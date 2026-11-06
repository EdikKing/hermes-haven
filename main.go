package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"hermes-haven/internal/web"
)

type ownedSession struct {
	Title    string `json:"title"`
	RunID    string `json:"run_id,omitempty"`
	External bool   `json:"external,omitempty"`
}
type state struct {
	Sessions map[string]ownedSession `json:"sessions"`
}
type server struct {
	mu                                                     sync.Mutex
	st                                                     state
	dataDir, hermesURL, apiKey                             string
	dashboardURL, authProvider                             string
	httpClient                                             *http.Client
	streamClient                                           *http.Client
	authClient                                             *http.Client
	logins                                                 map[string]loginSession
	loginAttempts                                          map[string][]time.Time
	secureCookie                                           bool
	imageRoots                                             []string
	historyDB, historyPython, historySource, historyScript string
	submissions                                            *submissionStore
	submitLocks                                            sync.Map
}

func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, code int, msg string) {
	jsonOut(w, code, map[string]any{"error": msg})
}
func readSecretEnv(path, key string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key+"=") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, key+"=")), "\"'"), nil
		}
	}
	return "", fmt.Errorf("未找到 %s", key)
}
func env(k, def string) string {
	if s := os.Getenv(k); s != "" {
		return s
	}
	return def
}
func newServer() (*server, error) {
	streamTransport := http.DefaultTransport.(*http.Transport).Clone()
	streamTransport.ResponseHeaderTimeout = 15 * time.Second
	s := &server{
		dataDir:       env("HAVEN_DATA_DIR", "data"),
		hermesURL:     strings.TrimRight(env("HERMES_URL", "http://127.0.0.1:8642"), "/"),
		dashboardURL:  strings.TrimRight(env("HERMES_DASHBOARD_URL", "http://127.0.0.1:9119"), "/"),
		authProvider:  env("HERMES_AUTH_PROVIDER", "basic"),
		historyDB:     os.Getenv("HERMES_HISTORY_DB"),
		historyPython: env("HERMES_HISTORY_PYTHON", "python3"),
		historySource: env("HERMES_SOURCE_DIR", "/usr/local/lib/hermes-agent"),
		apiKey:        os.Getenv("HERMES_API_KEY"),
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		streamClient:  &http.Client{Transport: streamTransport},
		authClient:    &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		logins:        map[string]loginSession{}, loginAttempts: map[string][]time.Time{},
		secureCookie: os.Getenv("HAVEN_SECURE_COOKIE") == "true",
	}
	if s.apiKey == "" && os.Getenv("HERMES_ENV_FILE") != "" {
		var err error
		s.apiKey, err = readSecretEnv(os.Getenv("HERMES_ENV_FILE"), "API_SERVER_KEY")
		if err != nil {
			return nil, err
		}
	}
	if s.apiKey == "" {
		return nil, errors.New("必须配置 Hermes API 密钥")
	}
	u, err := url.Parse(s.dashboardURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("Hermes Dashboard 地址无效")
	}
	if err := os.MkdirAll(filepath.Join(s.dataDir, "uploads"), 0700); err != nil {
		return nil, err
	}
	s.st.Sessions = map[string]ownedSession{}
	b, err := os.ReadFile(filepath.Join(s.dataDir, "state.json"))
	if err == nil {
		if err = json.Unmarshal(b, &s.st); err != nil {
			return nil, fmt.Errorf("读取状态失败: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if s.st.Sessions == nil {
		s.st.Sessions = map[string]ownedSession{}
	}
	s.submissions, err = openSubmissions(filepath.Join(s.dataDir, "requests"))
	if err != nil {
		return nil, fmt.Errorf("读取发送记录失败: %w", err)
	}
	if err = s.prepareHistoryReader(); err != nil {
		return nil, err
	}
	for _, root := range filepath.SplitList(env("HAVEN_IMAGE_ROOTS", "/root/.hermes-web-ui/upload:/root/.hermes/cache/documents/native-inputs")) {
		if root != "" {
			s.imageRoots = append(s.imageRoots, root)
		}
	}
	if s.historyDB != "" {
		_, code, probeErr := s.readOnlyMessages(context.Background(), "__haven_history_probe__", "0")
		if probeErr != nil || code != 404 {
			return nil, errors.New("无法确认 Hermes 只读历史能力，请检查历史读取配置")
		}
	}
	return s, nil
}
func (s *server) saveLocked() error {
	b, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dataDir, ".state-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(s.dataDir, "state.json"))
}
func (s *server) owned(id string) (ownedSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.st.Sessions[id]
	return v, ok
}
func (s *server) setOwned(id string, v ownedSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.st.Sessions[id]
	s.st.Sessions[id] = v
	if err := s.saveLocked(); err != nil {
		if ok {
			s.st.Sessions[id] = old
		} else {
			delete(s.st.Sessions, id)
		}
		return err
	}
	return nil
}
func (s *server) upstream(ctx context.Context, method, path string, body any, extra http.Header) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return nil, e
		}
		reader = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, s.hermesURL+path, reader)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range extra {
		for _, x := range v {
			req.Header.Add(k, x)
		}
	}
	return s.httpClient.Do(req)
}
func (s *server) proxyJSON(w http.ResponseWriter, r *http.Request, method, path string, body any) {
	res, err := s.upstream(r.Context(), method, path, body, nil)
	if err != nil {
		fail(w, 502, "无法连接 Hermes")
		return
	}
	defer res.Body.Close()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(res.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(res.Body, 12<<20))
}
func (s *server) require(w http.ResponseWriter, r *http.Request) bool {
	c, err := r.Cookie("haven_session")
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.logins[c.Value]
	if !ok || !time.Now().Before(session.ExpiresAt) {
		delete(s.logins, c.Value)
		return false
	}
	return true
}
func (s *server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, e := url.Parse(origin)
	return e == nil && strings.EqualFold(u.Host, r.Host) && (u.Scheme == "http" || u.Scheme == "https")
}
func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || !s.sameOrigin(r) {
		fail(w, 403, "请求来源无效")
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	s.mu.Lock()
	now := time.Now()
	var recent []time.Time
	for _, t := range s.loginAttempts[ip] {
		if now.Sub(t) < time.Minute*10 {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 8 {
		s.mu.Unlock()
		fail(w, 429, "尝试次数过多，请稍后重试")
		return
	}
	// 先计数再验证，避免并发请求绕过登录频率限制。
	s.loginAttempts[ip] = append(recent, now)
	s.mu.Unlock()
	var b struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b) != nil {
		fail(w, 400, "请求无效")
		return
	}
	if strings.TrimSpace(b.Username) == "" || b.Password == "" {
		fail(w, 400, "请输入 Hermes 用户名和密码")
		return
	}
	session, code, msg := s.authenticate(r.Context(), b.Username, b.Password)
	if code != http.StatusOK {
		fail(w, code, msg)
		return
	}
	token := randomID(32)
	s.mu.Lock()
	delete(s.loginAttempts, ip)
	if old, err := r.Cookie("haven_session"); err == nil {
		delete(s.logins, old.Value)
	}
	for id, login := range s.logins {
		if !now.Before(login.ExpiresAt) {
			delete(s.logins, id)
		}
	}
	s.logins[token] = session
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "haven_session", Value: token, Path: "/", HttpOnly: true, Secure: s.secureCookie, SameSite: http.SameSiteStrictMode, MaxAge: max(1, int(time.Until(session.ExpiresAt).Seconds())), Expires: session.ExpiresAt})
	jsonOut(w, 200, session.identity())
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)

func validID(x string) bool { return safeID.MatchString(x) }
func (s *server) list(w http.ResponseWriter, r *http.Request) {
	offset, err := strconv.Atoi(envQuery(r, "offset", "0"))
	if err != nil || offset < 0 {
		fail(w, 400, "分页位置无效")
		return
	}
	res, err := s.upstream(r.Context(), "GET", "/api/sessions?limit=200&offset="+strconv.Itoa(offset), nil, nil)
	if err != nil {
		fail(w, 502, "无法连接 Hermes")
		return
	}
	defer res.Body.Close()
	var data map[string]any
	if json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&data) != nil {
		fail(w, 502, "Hermes 响应无效")
		return
	}
	if res.StatusCode != 200 {
		jsonOut(w, res.StatusCode, data)
		return
	}
	rows, _ := data["data"].([]any)
	for _, v := range rows {
		row, ok := v.(map[string]any)
		if !ok {
			continue
		}
		id, _ := row["id"].(string)
		own, tracked := s.owned(id)
		row["owned"] = tracked && !own.External
		if tracked {
			if !own.External {
				row["haven_title"] = own.Title
			}
			row["run_id"] = own.RunID
		}
	}
	jsonOut(w, 200, data)
}
func (s *server) create(w http.ResponseWriter, r *http.Request) {
	id := "haven_" + randomID(16)
	res, err := s.upstream(r.Context(), "POST", "/api/sessions", map[string]any{"id": id}, nil)
	if err != nil {
		fail(w, 502, "创建会话失败")
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 201 {
		fail(w, 502, "Hermes 未能创建会话")
		return
	}
	if err = s.setOwned(id, ownedSession{Title: "新会话"}); err != nil {
		fail(w, 500, "会话已创建但本地登记失败，已禁止写入该会话")
		return
	}
	jsonOut(w, 201, map[string]any{"id": id, "title": "新会话"})
}
func (s *server) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
	if err := r.ParseMultipartForm(6 << 20); err != nil {
		fail(w, 400, "图片大小不能超过 6 MB")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	f, h, err := r.FormFile("file")
	if err != nil {
		fail(w, 400, "请选择图片")
		return
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 5<<20+1))
	if err != nil || len(b) > 5<<20 {
		fail(w, 400, "图片大小不能超过 5 MB")
		return
	}
	mt := http.DetectContentType(b)
	if mt != "image/png" && mt != "image/jpeg" && mt != "image/gif" && mt != "image/webp" {
		fail(w, 400, "仅支持 PNG、JPEG、GIF 和 WebP 图片")
		return
	}
	id := randomID(20)
	p := filepath.Join(s.dataDir, "uploads", id)
	if err = os.WriteFile(p, b, 0600); err != nil {
		fail(w, 500, "保存图片失败")
		return
	}
	jsonOut(w, 201, map[string]any{"id": id, "name": filepath.Base(h.Filename), "mime": mt})
}
func (s *server) send(w http.ResponseWriter, r *http.Request, id string) {
	var b struct {
		Text      string   `json:"text"`
		Images    []string `json:"images"`
		RequestID string   `json:"request_id"`
		CreatedAt int64    `json:"created_at"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&b) != nil {
		fail(w, 400, "请求无效")
		return
	}
	b.Text = strings.TrimSpace(b.Text)
	if (b.Text == "" && len(b.Images) == 0) || len(b.Text) > 30000 || len(b.Images) > 4 || !validID(b.RequestID) {
		fail(w, 400, "消息、图片或请求编号无效")
		return
	}
	var input any = b.Text
	if len(b.Images) > 0 {
		parts := []any{}
		if b.Text != "" {
			parts = append(parts, map[string]any{"type": "text", "text": b.Text})
		}
		total := 0
		for _, img := range b.Images {
			if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(img) {
				fail(w, 400, "图片编号无效")
				return
			}
			raw, e := os.ReadFile(filepath.Join(s.dataDir, "uploads", img))
			if e != nil || len(raw) > 5<<20 {
				fail(w, 400, "图片不可用")
				return
			}
			total += len(raw)
			if total > 6<<20 {
				fail(w, 400, "图片总大小不能超过 6 MB")
				return
			}
			mt := http.DetectContentType(raw)
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:" + mt + ";base64," + base64.StdEncoding.EncodeToString(raw)}})
		}
		input = []any{map[string]any{"role": "user", "content": parts}}
	}
	s.submit(w, r, id, b.RequestID, b.Text, b.Images, b.CreatedAt, input)
}
func (s *server) events(w http.ResponseWriter, r *http.Request, id string) {
	own, ok := s.owned(id)
	run := r.URL.Query().Get("run_id")
	if run == "" || !validID(run) || ((!ok || run != own.RunID) && !s.submissions.hasRun(id, run)) {
		fail(w, 403, "运行不可访问")
		return
	}
	h := http.Header{}
	if last := r.Header.Get("Last-Event-ID"); last != "" {
		h.Set("Last-Event-ID", last)
	}
	if last := r.URL.Query().Get("last_seq"); last != "" {
		h.Set("Last-Event-ID", last)
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if c, e := r.Cookie("haven_session"); e == nil {
		s.mu.Lock()
		expires := s.logins[c.Value].ExpiresAt
		s.mu.Unlock()
		timer := time.AfterFunc(max(0, time.Until(expires)), cancel)
		defer timer.Stop()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", s.hermesURL+"/v1/runs/"+url.PathEscape(run)+"/events", nil)
	if err != nil {
		fail(w, 500, "请求无效")
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	for k, v := range h {
		req.Header[k] = v
	}
	res, err := s.streamClient.Do(req)
	if err != nil {
		fail(w, 502, "无法连接运行事件")
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		fail(w, res.StatusCode, "运行事件不可用")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	f, ok := w.(http.Flusher)
	if !ok {
		return
	}
	buf := make([]byte, 4096)
	for {
		n, e := res.Body.Read(buf)
		if n > 0 {
			if _, err = w.Write(buf[:n]); err != nil {
				return
			}
			f.Flush()
		}
		if e != nil {
			return
		}
	}
}
func (s *server) api(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/api/login" {
		s.login(w, r)
		return
	}
	if !s.require(w, r) {
		jsonOut(w, 401, map[string]any{"error": "登录已过期，请重新登录", "code": "login_required"})
		return
	}
	if r.Method != "GET" && !s.sameOrigin(r) {
		fail(w, 403, "请求来源无效")
		return
	}
	switch r.URL.Path {
	case "/api/me":
		c, _ := r.Cookie("haven_session")
		s.mu.Lock()
		session := s.logins[c.Value]
		s.mu.Unlock()
		jsonOut(w, 200, session.identity())
		return
	case "/api/logout":
		if r.Method == "POST" {
			c, _ := r.Cookie("haven_session")
			if c != nil {
				s.mu.Lock()
				delete(s.logins, c.Value)
				s.mu.Unlock()
			}
			http.SetCookie(w, &http.Cookie{Name: "haven_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
			jsonOut(w, 200, map[string]bool{"ok": true})
			return
		}
	case "/api/health":
		s.proxyJSON(w, r, "GET", "/v1/capabilities", nil)
		return
	case "/api/sessions":
		if r.Method == "GET" {
			s.list(w, r)
			return
		}
		if r.Method == "POST" {
			s.create(w, r)
			return
		}
	case "/api/uploads":
		if r.Method == "POST" {
			s.upload(w, r)
			return
		}
	}
	if strings.HasPrefix(r.URL.Path, "/api/uploads/") && r.Method == "GET" {
		s.uploadImage(w, r, strings.TrimPrefix(r.URL.Path, "/api/uploads/"))
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/")
	if !strings.HasPrefix(r.URL.Path, "/api/sessions/") || len(parts) < 2 || !validID(parts[0]) {
		fail(w, 404, "未找到")
		return
	}
	id := parts[0]
	switch {
	case len(parts) == 2 && parts[1] == "messages" && r.Method == "GET":
		s.messages(w, r, id)
	case len(parts) == 2 && parts[1] == "runs" && r.Method == "POST":
		s.send(w, r, id)
	case len(parts) == 2 && parts[1] == "events" && r.Method == "GET":
		s.events(w, r, id)
	case len(parts) == 2 && parts[1] == "status" && r.Method == "GET":
		s.sessionStatus(w, r, id)
	case len(parts) == 3 && parts[1] == "requests" && validID(parts[2]) && r.Method == "GET":
		s.requestStatus(w, r, id, parts[2])
	case len(parts) == 4 && parts[1] == "requests" && validID(parts[2]) && parts[3] == "acknowledge" && r.Method == "POST":
		s.acknowledgeSubmission(w, r, id, parts[2])
	case len(parts) == 2 && parts[1] == "stop" && r.Method == "POST":
		run := s.sessionRun(id)
		if run == "" {
			fail(w, 409, "此会话没有可停止的 Haven 运行")
			return
		}
		s.proxyJSON(w, r, "POST", "/v1/runs/"+url.PathEscape(run)+"/stop", map[string]any{})
	case len(parts) == 2 && parts[1] == "title" && r.Method == "POST":
		own, ok := s.owned(id)
		if !ok || own.External {
			fail(w, 403, "无法修改历史会话标题")
			return
		}
		var b struct {
			Title string `json:"title"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b) != nil || strings.TrimSpace(b.Title) == "" || len([]rune(b.Title)) > 80 {
			fail(w, 400, "标题无效")
			return
		}
		own.Title = strings.TrimSpace(b.Title)
		if s.setOwned(id, own) != nil {
			fail(w, 500, "保存失败")
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	case len(parts) == 4 && parts[1] == "images" && r.Method == "GET":
		s.historyImage(w, r, id, parts[2], parts[3])
	default:
		fail(w, 404, "未找到")
	}
}

var imageMarker = regexp.MustCompile(`\[Image attached at: ([^\]\r\n]+)\]`)

func (s *server) fetchMessages(ctx context.Context, id, offset string) (map[string]any, int, error) {
	if n, err := strconv.Atoi(offset); err != nil || n < 0 {
		offset = "0"
	}
	if s.historyDB != "" {
		return s.readOnlyMessages(ctx, id, offset)
	}
	// 未配置只读数据库时只取活动消息，避免官方完整显示接口回填旧记录。
	res, err := s.upstream(ctx, "GET", "/api/sessions/"+url.PathEscape(id)+"/messages?limit=100&order=latest&include_compacted=false&offset="+offset, nil, nil)
	if err != nil {
		return nil, 502, err
	}
	defer res.Body.Close()
	var v map[string]any
	err = json.NewDecoder(io.LimitReader(res.Body, 12<<20)).Decode(&v)
	if own, ok := s.owned(id); v != nil && (!ok || own.External) {
		v["history_notice"] = "完整旧历史暂未启用，当前仅显示活动消息。"
	}
	return v, res.StatusCode, err
}
func (s *server) scrubContent(content any, id string, mid any) any {
	if blocks, ok := contentBlocks(content); ok {
		for i, b := range blocks {
			if b["type"] == "image" {
				path, _ := b["path"].(string)
				delete(b, "path")
				delete(b, "url")
				b["src"] = s.imageReference(id, fmt.Sprint(mid), i, path)
			}
		}
		return blocks
	}
	text, ok := content.(string)
	if !ok {
		return content
	}
	n := 0
	return imageMarker.ReplaceAllStringFunc(text, func(marker string) string {
		path := imageMarker.FindStringSubmatch(marker)[1]
		v := "![图片](" + s.imageReference(id, fmt.Sprint(mid), n, path) + ")"
		n++
		return v
	})
}
func (s *server) messages(w http.ResponseWriter, r *http.Request, id string) {
	v, code, err := s.fetchMessages(r.Context(), id, r.URL.Query().Get("offset"))
	if err != nil {
		fail(w, 502, "无法读取历史")
		return
	}
	if rows, ok := v["data"].([]any); ok {
		for _, x := range rows {
			if m, ok := x.(map[string]any); ok {
				m["content"] = s.scrubContent(m["content"], id, m["id"])
			}
		}
	}
	jsonOut(w, code, v)
}
func (s *server) historyImage(w http.ResponseWriter, r *http.Request, id, mid, idx string) {
	i, err := strconv.Atoi(idx)
	if err != nil || i < 0 {
		fail(w, 400, "图片编号无效")
		return
	}
	var path string
	if token := r.URL.Query().Get("ref"); token != "" {
		var e error
		path, e = s.readImageReference(token, id, mid, i)
		if e != nil {
			fail(w, 403, "图片引用无效，请刷新会话")
			return
		}
	}
	// 新链接直接使用加密的消息引用；兼容旧链接时按页查找，不再限定历史长度。
	lastPage := ""
	for offset := 0; path == ""; offset += 100 {
		v, code, e := s.fetchMessages(r.Context(), id, strconv.Itoa(offset))
		if e != nil || code != 200 {
			break
		}
		rows, _ := v["data"].([]any)
		if len(rows) == 0 {
			break
		}
		pageKey := fmt.Sprint(rows[0])
		if pageKey == lastPage {
			break
		}
		lastPage = pageKey
		for _, x := range rows {
			m, ok := x.(map[string]any)
			if !ok || fmt.Sprint(m["id"]) != mid {
				continue
			}
			text, _ := m["content"].(string)
			if blocks, ok := contentBlocks(m["content"]); ok && i < len(blocks) {
				path, _ = blocks[i]["path"].(string)
			} else {
				matches := imageMarker.FindAllStringSubmatch(text, -1)
				if i < len(matches) {
					path = matches[i][1]
				}
			}
			break
		}
		if path != "" || len(rows) < 100 {
			break
		}
	}
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil {
		fail(w, 404, "图片不可用")
		return
	}
	allowed := false
	for _, root := range s.imageRoots {
		base, e := filepath.EvalSymlinks(root)
		if e != nil {
			continue
		}
		rel, e := filepath.Rel(base, resolved)
		if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel) {
			allowed = true
			break
		}
	}
	if !allowed {
		fail(w, 403, "图片不在允许的目录中")
		return
	}
	f, e := os.Open(resolved)
	if e != nil {
		fail(w, 404, "图片不可用")
		return
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > 10<<20 {
		fail(w, 404, "图片不可用")
		return
	}
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	mt := http.DetectContentType(buf[:n])
	if !strings.HasPrefix(mt, "image/") || mt == "image/svg+xml" {
		fail(w, 415, "文件类型不支持")
		return
	}
	_, _ = f.Seek(0, 0)
	w.Header().Set("Content-Type", mt)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, f)
}
func main() {
	s, err := newServer()
	if err != nil {
		log.Fatal(err)
	}
	dist, err := fs.Sub(web.Files, "dist")
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", s.api)
	mux.Handle("/", http.FileServer(http.FS(dist)))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		mux.ServeHTTP(w, r)
	})
	addr := env("HAVEN_LISTEN", "127.0.0.1:18770")
	log.Printf("Hermes Haven 启动于 %s", addr)
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
