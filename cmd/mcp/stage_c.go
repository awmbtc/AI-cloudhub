package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

func toolListMarketplace(api, token string, mine bool) (interface{}, error) {
	u := api + "/v1/marketplace"
	if mine {
		u += "?mine=1"
	}
	body, code, err := httpDo(http.MethodGet, u, token, nil)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("list marketplace HTTP %d: %s", code, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolInstallMarketplace(api, token, itemID string) (interface{}, error) {
	body, code, err := httpDo(http.MethodPost, api+"/v1/marketplace/"+url.PathEscape(itemID)+"/install", token, map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("install marketplace HTTP %d: %s", code, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolListMemory(api, token, layer, key, driveID string, limit int) (interface{}, error) {
	q := url.Values{}
	if layer != "" {
		q.Set("layer", layer)
	}
	if key != "" {
		q.Set("key", key)
	}
	if driveID != "" {
		q.Set("drive_id", driveID)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	u := api + "/v1/memory"
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	body, code, err := httpDo(http.MethodGet, u, token, nil)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("list memory HTTP %d: %s", code, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolPutMemory(api, token, layer, content, key, driveID string, meta map[string]interface{}, embedding []float64, ttlSec int) (interface{}, error) {
	payload := map[string]interface{}{
		"content": content,
	}
	if layer != "" {
		payload["layer"] = layer
	}
	if key != "" {
		payload["key"] = key
	}
	if driveID != "" {
		payload["drive_id"] = driveID
	}
	if meta != nil {
		payload["meta"] = meta
	}
	if len(embedding) > 0 {
		payload["embedding"] = embedding
	}
	if ttlSec > 0 {
		payload["ttl_sec"] = ttlSec
	}
	body, code, err := httpDo(http.MethodPost, api+"/v1/memory", token, payload)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("put memory HTTP %d: %s", code, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolSearchMemory(api, token string, query []float64, k int, layer string) (interface{}, error) {
	payload := map[string]interface{}{"query": query}
	if k > 0 {
		payload["k"] = k
	}
	if layer != "" {
		payload["layer"] = layer
	}
	body, code, err := httpDo(http.MethodPost, api+"/v1/memory/search", token, payload)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("search memory HTTP %d: %s", code, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolListGraph(api, token, subject, object string, limit int) (interface{}, error) {
	q := url.Values{}
	if subject != "" {
		q.Set("subject", subject)
	}
	if object != "" {
		q.Set("object", object)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	u := api + "/v1/graph"
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	body, code, err := httpDo(http.MethodGet, u, token, nil)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("list graph HTTP %d: %s", code, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolLinkGraph(api, token, subject, relation, object string, meta map[string]interface{}) (interface{}, error) {
	payload := map[string]interface{}{
		"subject":  subject,
		"relation": relation,
		"object":   object,
	}
	if meta != nil {
		payload["meta"] = meta
	}
	body, code, err := httpDo(http.MethodPost, api+"/v1/graph", token, payload)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("link graph HTTP %d: %s", code, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolListConnectors(api, token string) (interface{}, error) {
	body, code, err := httpDo(http.MethodGet, api+"/v1/connectors", token, nil)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("list connectors HTTP %d: %s", code, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolConnectorsCatalog(api, token string) (interface{}, error) {
	body, code, err := httpDo(http.MethodGet, api+"/v1/connectors/catalog", token, nil)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("connectors catalog HTTP %d: %s", code, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolListLineage(api, token, entity string, limit int) (interface{}, error) {
	q := url.Values{}
	if entity != "" {
		q.Set("entity", entity)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	u := api + "/v1/lineage"
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	body, code, err := httpDo(http.MethodGet, u, token, nil)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("list lineage HTTP %d: %s", code, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}

func toolRecordLineage(api, token, action, entity, parent, detail string) (interface{}, error) {
	payload := map[string]interface{}{
		"action": action,
		"entity": entity,
	}
	if parent != "" {
		payload["parent"] = parent
	}
	if detail != "" {
		payload["detail"] = detail
	}
	body, code, err := httpDo(http.MethodPost, api+"/v1/lineage", token, payload)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("record lineage HTTP %d: %s", code, truncate(string(body), 512))
	}
	var parsed interface{}
	_ = json.Unmarshal(body, &parsed)
	return toolResultJSON(parsed)
}
