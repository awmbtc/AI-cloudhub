package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// connectorMaterialization is the BYOC outcome of materializing a connector.
type connectorMaterialization struct {
	Note       string
	ClonePath  string
	ExtraEnv   map[string]string
	PassLibpq  bool
	PassMysql  bool
	Err        error
	StrictHint string // clone | pg | mysql | any
}

// materializeConnector fetches the job/env connector and applies type-specific BYOC steps.
// Secrets never come from the control plane — only non-secret config + host env.
func materializeConnector(api, token, mountPoint string) connectorMaterialization {
	cid := strings.TrimSpace(os.Getenv("AI_CLOUDHUB_CONNECTOR_ID"))
	if cid == "" {
		return connectorMaterialization{}
	}
	typ, cfg, err := fetchConnector(api, token, cid)
	if err != nil {
		return connectorMaterialization{Err: err, Note: "connector failed: " + err.Error(), StrictHint: "any"}
	}
	switch strings.ToLower(typ) {
	case "git":
		dest, gerr := applyGitClone(cfg, mountPoint)
		if gerr != nil {
			return connectorMaterialization{Err: gerr, Note: "clone failed: " + gerr.Error(), StrictHint: "clone"}
		}
		if dest == "" {
			return connectorMaterialization{}
		}
		return connectorMaterialization{Note: "cloned to " + dest, ClonePath: dest}
	case "postgres":
		extra, note, perr := applyPostgresEnv(cfg)
		if perr != nil {
			return connectorMaterialization{Err: perr, Note: "pg failed: " + perr.Error(), StrictHint: "pg"}
		}
		return connectorMaterialization{Note: note, ExtraEnv: extra, PassLibpq: true}
	case "mysql":
		extra, note, merr := applyMysqlEnv(cfg)
		if merr != nil {
			return connectorMaterialization{Err: merr, Note: "mysql failed: " + merr.Error(), StrictHint: "mysql"}
		}
		return connectorMaterialization{Note: note, ExtraEnv: extra, PassMysql: true}
	default:
		log.Printf("connector %s type=%s (no materializer)", cid, typ)
		return connectorMaterialization{}
	}
}

func fetchConnector(api, token, cid string) (typ string, cfg map[string]interface{}, err error) {
	req, err := http.NewRequest(http.MethodGet, api+"/v1/connectors/"+cid, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return "", nil, fmt.Errorf("GET connector HTTP %d: %s", res.StatusCode, body)
	}
	var c struct {
		Type   string          `json:"type"`
		Name   string          `json:"name"`
		Config json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(body, &c); err != nil {
		return "", nil, err
	}
	cfg = map[string]interface{}{}
	if len(c.Config) > 0 {
		_ = json.Unmarshal(c.Config, &cfg)
	}
	return c.Type, cfg, nil
}

func applyGitClone(cfg map[string]interface{}, mountPoint string) (dest string, err error) {
	remote, _ := cfg["remote_url"].(string)
	if remote == "" {
		remote, _ = cfg["url"].(string)
	}
	if remote == "" {
		return "", fmt.Errorf("git connector missing remote_url")
	}
	branch, _ := cfg["branch"].(string)
	prefix, _ := cfg["path_prefix"].(string)
	if prefix != "" {
		dest = filepath.Join(mountPoint, prefix)
	} else {
		dest = filepath.Join(mountPoint, "repo")
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		log.Printf("git connector: already cloned at %s", dest)
		return dest, nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git not in PATH")
	}
	_ = os.MkdirAll(filepath.Dir(dest), 0o755)
	args := []string{"clone", "--depth", "1"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, remote, dest)
	log.Printf("git connector: clone %s -> %s", remote, dest)
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git clone: %w", err)
	}
	return dest, nil
}

// applyPostgresEnv builds non-secret AI_CLOUDHUB_PG_* env for the agent.
// Password must come from host PGPASSWORD (PassLibpq), never config.
func applyPostgresEnv(cfg map[string]interface{}) (extra map[string]string, note string, err error) {
	host := cfgStr(cfg, "host")
	database := cfgStr(cfg, "database")
	if host == "" || database == "" {
		return nil, "", fmt.Errorf("postgres connector missing host or database")
	}
	// Defense in depth: ignore secret keys if present.
	port := cfgStr(cfg, "port")
	if port == "" {
		port = "5432"
	}
	user := cfgStr(cfg, "user")
	schema := cfgStr(cfg, "schema")
	sslmode := cfgStr(cfg, "sslmode")
	if sslmode == "" {
		sslmode = "prefer"
	}
	tmpl := cfgStr(cfg, "dsn_template")
	if tmpl != "" && dsnLooksSecret(tmpl) {
		return nil, "", fmt.Errorf("dsn_template must not embed credentials")
	}
	if tmpl == "" {
		if user != "" {
			tmpl = fmt.Sprintf("postgres://%s@%s:%s/%s?sslmode=%s", user, host, port, database, sslmode)
		} else {
			tmpl = fmt.Sprintf("postgres://%s:%s/%s?sslmode=%s", host, port, database, sslmode)
		}
	}
	extra = map[string]string{
		"AI_CLOUDHUB_PG_HOST":         host,
		"AI_CLOUDHUB_PG_PORT":         port,
		"AI_CLOUDHUB_PG_DATABASE":     database,
		"AI_CLOUDHUB_PG_SSLMODE":      sslmode,
		"AI_CLOUDHUB_PG_DSN_TEMPLATE": tmpl,
	}
	if user != "" {
		extra["AI_CLOUDHUB_PG_USER"] = user
	}
	if schema != "" {
		extra["AI_CLOUDHUB_PG_SCHEMA"] = schema
	}
	note = fmt.Sprintf("pg ready host=%s db=%s", host, database)
	log.Printf("postgres connector: %s", note)
	return extra, note, nil
}

func cfgStr(cfg map[string]interface{}, key string) string {
	if cfg == nil {
		return ""
	}
	if s, ok := cfg[key].(string); ok {
		return strings.TrimSpace(s)
	}
	if n, ok := cfg[key].(float64); ok {
		return strings.TrimSpace(fmt.Sprintf("%.0f", n))
	}
	return ""
}

func dsnLooksSecret(tmpl string) bool {
	t := strings.ToLower(tmpl)
	if strings.Contains(t, "password=") {
		return true
	}
	if i := strings.Index(t, "://"); i >= 0 {
		rest := t[i+3:]
		if at := strings.Index(rest, "@"); at > 0 {
			if strings.Contains(rest[:at], ":") {
				return true
			}
		}
	}
	return false
}

func pgStrict() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("AI_CLOUDHUB_PG_STRICT")))
	return v == "1" || v == "true" || v == "yes"
}

// applyMysqlEnv builds non-secret AI_CLOUDHUB_MYSQL_* env for the agent.
// Password must come from host MYSQL_PWD (PassMysql), never config.
func applyMysqlEnv(cfg map[string]interface{}) (extra map[string]string, note string, err error) {
	host := cfgStr(cfg, "host")
	database := cfgStr(cfg, "database")
	if host == "" || database == "" {
		return nil, "", fmt.Errorf("mysql connector missing host or database")
	}
	port := cfgStr(cfg, "port")
	if port == "" {
		port = "3306"
	}
	user := cfgStr(cfg, "user")
	params := cfgStr(cfg, "params")
	tmpl := cfgStr(cfg, "dsn_template")
	if tmpl != "" && dsnLooksSecret(tmpl) {
		return nil, "", fmt.Errorf("dsn_template must not embed credentials")
	}
	if tmpl == "" {
		// Go-sql-driver style without password: user@tcp(host:port)/db
		u := user
		if u == "" {
			u = "root"
		}
		tmpl = fmt.Sprintf("%s@tcp(%s:%s)/%s", u, host, port, database)
		if params != "" {
			tmpl += "?" + params
		}
	}
	extra = map[string]string{
		"AI_CLOUDHUB_MYSQL_HOST":         host,
		"AI_CLOUDHUB_MYSQL_PORT":         port,
		"AI_CLOUDHUB_MYSQL_DATABASE":     database,
		"AI_CLOUDHUB_MYSQL_DSN_TEMPLATE": tmpl,
	}
	if user != "" {
		extra["AI_CLOUDHUB_MYSQL_USER"] = user
	}
	if params != "" {
		extra["AI_CLOUDHUB_MYSQL_PARAMS"] = params
	}
	note = fmt.Sprintf("mysql ready host=%s db=%s", host, database)
	log.Printf("mysql connector: %s", note)
	return extra, note, nil
}

func mysqlStrict() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("AI_CLOUDHUB_MYSQL_STRICT")))
	return v == "1" || v == "true" || v == "yes"
}
