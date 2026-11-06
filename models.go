package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode"
)

type modelSettings struct {
	Model           string `json:"model,omitempty"`
	Provider        string `json:"provider,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

func (v modelSettings) valid() bool {
	validText := func(text string, limit int) bool {
		return len(text) <= limit && text == strings.TrimSpace(text) && !strings.ContainsFunc(text, unicode.IsControl)
	}
	if !validText(v.Model, 512) || !validText(v.Provider, 80) || (v.Provider != "" && v.Model == "") {
		return false
	}
	switch v.ReasoningEffort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
		return true
	}
	return false
}

func (v modelSettings) apply(payload map[string]any) {
	if v.Model != "" {
		payload["model"] = v.Model
	}
	if v.Provider != "" {
		payload["provider"] = v.Provider
	}
	if v.ReasoningEffort != "" {
		reasoning := map[string]any{"enabled": true, "effort": v.ReasoningEffort}
		if v.ReasoningEffort == "none" {
			reasoning = map[string]any{"enabled": false}
		}
		payload["model_options"] = map[string]any{"reasoning": reasoning}
	}
}

func runPayload(id string, input any, settings *modelSettings) map[string]any {
	payload := map[string]any{"session_id": id, "input": input}
	if settings != nil {
		settings.apply(payload)
	}
	return payload
}

func (s *server) savedSettings(id string) modelSettings {
	own, _ := s.owned(id)
	if own.Settings != nil {
		return *own.Settings
	}
	return modelSettings{}
}

func (s *server) sessionSettings(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method == http.MethodGet {
		jsonOut(w, 200, s.savedSettings(id))
		return
	}
	var settings modelSettings
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&settings) != nil || !settings.valid() {
		fail(w, 400, "模型或思考强度无效")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, exists := s.st.Sessions[id]
	own := old
	if !exists {
		own.External = true
	}
	own.Settings = &settings
	s.st.Sessions[id] = own
	if s.saveLocked() != nil {
		if exists {
			s.st.Sessions[id] = old
		} else {
			delete(s.st.Sessions, id)
		}
		fail(w, 500, "设置保存失败，请重试")
		return
	}
	jsonOut(w, 200, settings)
}

type modelCapability struct {
	Reasoning           *bool `json:"reasoning,omitempty"`
	CanDisableReasoning *bool `json:"can_disable_reasoning,omitempty"`
}

type modelProvider struct {
	Slug          string                     `json:"slug"`
	Name          string                     `json:"name"`
	Models        []string                   `json:"models"`
	Capabilities  map[string]modelCapability `json:"capabilities,omitempty"`
	Authenticated *bool                      `json:"authenticated,omitempty"`
}

func (s *server) models(w http.ResponseWriter, r *http.Request) {
	res, err := s.upstream(r.Context(), "GET", "/api/model/options?include_unconfigured=false", nil, nil)
	if err != nil {
		fail(w, 502, "无法加载模型列表，请稍后重试")
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		fail(w, 502, "Hermes 暂未提供模型列表，请稍后重试")
		return
	}
	// 只转交选择器需要的公开字段，避免把供应商地址或认证信息带到浏览器。
	var catalog struct {
		Model     string          `json:"model"`
		Provider  string          `json:"provider"`
		Providers []modelProvider `json:"providers"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&catalog) != nil {
		fail(w, 502, "Hermes 模型列表无效")
		return
	}
	providers := make([]modelProvider, 0, len(catalog.Providers))
	for _, provider := range catalog.Providers {
		if provider.Slug != "" && len(provider.Models) > 0 && (provider.Authenticated == nil || *provider.Authenticated) {
			providers = append(providers, provider)
		}
	}
	catalog.Providers = providers
	jsonOut(w, 200, catalog)
}
