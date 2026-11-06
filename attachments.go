package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

const maxFileBytes int64 = 50 << 20
const maxAttachmentBytes int64 = 100 << 20
const maxTextPreview = 1 << 20

type attachment struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	MIME        string    `json:"mime"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	Kind        string    `json:"kind"`
	PreviewKind string    `json:"preview_kind"`
	CreatedAt   time.Time `json:"created_at"`
	StorageName string    `json:"storage_name"`
}

func (a attachment) public() map[string]any {
	return map[string]any{"id": a.ID, "name": a.Name, "mime": a.MIME, "size": a.Size, "sha256": a.SHA256,
		"kind": a.Kind, "preview_kind": a.PreviewKind, "download_url": "/api/uploads/" + a.ID + "/download",
		"preview_url": "/api/uploads/" + a.ID + "/preview"}
}

func imageMIME(mt string) bool {
	return mt == "image/png" || mt == "image/jpeg" || mt == "image/gif" || mt == "image/webp"
}

func attachmentName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name))
	if name == "" || name == "." || name == ".." {
		name = "附件"
	}
	runes := []rune(name)
	if len(runes) > 160 {
		name = string(runes[:140]) + filepath.Ext(name)
	}
	return name
}

func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func writeAtomicJSON(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".metadata-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = f.Chmod(0600)
	if err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

// 文件只在内容、摘要和元数据全部同步后发布；失败目录尚未被任何请求引用。
func (s *server) storeAttachment(reader io.Reader, name string, enforceImageLimit bool) (attachment, error) {
	var a attachment
	base := filepath.Join(s.dataDir, "files")
	if err := os.MkdirAll(base, 0700); err != nil {
		return a, err
	}
	dir, err := os.MkdirTemp(base, ".upload-*")
	if err != nil {
		return a, err
	}
	defer os.RemoveAll(dir)
	name = attachmentName(name)
	ext := filepath.Ext(name)
	if len(ext) > 24 || strings.ContainsAny(ext, "/\\:") {
		ext = ""
	}
	filename := "content" + ext
	f, err := os.OpenFile(filepath.Join(dir, filename), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return a, err
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(reader, maxFileBytes+1))
	if err == nil && n > maxFileBytes {
		err = errors.New("文件大小不能超过 50 MiB")
	}
	if err == nil {
		err = f.Sync()
	}
	buf := make([]byte, 512)
	var size int
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
		if err == nil {
			size, err = f.Read(buf)
			if errors.Is(err, io.EOF) {
				err = nil
			}
		}
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return a, err
	}
	mt, kind, preview := http.DetectContentType(buf[:size]), "file", "none"
	if imageMIME(mt) {
		if enforceImageLimit && n > 5<<20 {
			return a, errors.New("图片大小不能超过 5 MiB")
		}
		kind, preview = "image", "image"
	} else if mt == "application/pdf" {
		preview = "pdf"
	} else if bytes.HasPrefix(buf[:size], []byte{0xff, 0xfe}) || bytes.HasPrefix(buf[:size], []byte{0xfe, 0xff}) {
		preview = "text"
	}
	// 文本边界可能恰好截断 UTF-8；文本类型在预览时再做完整的编码检查。
	if strings.HasPrefix(mt, "text/") {
		preview = "text"
	}
	a = attachment{ID: randomID(20), Name: name, MIME: mt, Size: n, SHA256: hex.EncodeToString(hash.Sum(nil)), Kind: kind, PreviewKind: preview, CreatedAt: time.Now().UTC(), StorageName: filename}
	if err = writeAtomicJSON(filepath.Join(dir, "metadata.json"), a); err != nil {
		return attachment{}, err
	}
	if err = os.Rename(dir, filepath.Join(base, a.ID)); err != nil {
		return attachment{}, err
	}
	if err = syncDirectory(base); err != nil {
		return attachment{}, err
	}
	return a, nil
}

// 使用受限根目录打开，拒绝文件路径及父目录通过符号链接逃逸。
func openConfined(base, relative string) (*os.File, error) {
	if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(filepath.Clean(relative), ".."+string(os.PathSeparator)) {
		return nil, errors.New("文件路径越界")
	}
	abs, err := filepath.Abs(base)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	equal := filepath.Clean(resolved) == filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		equal = strings.EqualFold(filepath.Clean(resolved), filepath.Clean(abs))
	}
	if !equal {
		return nil, errors.New("文件目录包含符号链接")
	}
	st, err := os.Lstat(filepath.Join(abs, relative))
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("文件不是普通文件")
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(relative)
	if err != nil {
		return nil, err
	}
	st, err = f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("文件不是普通文件")
	}
	return f, nil
}

func (s *server) attachment(id string) (attachment, error) {
	var a attachment
	if !uploadID.MatchString(id) {
		return a, errors.New("附件编号无效")
	}
	f, err := openConfined(filepath.Join(s.dataDir, "files", id), "metadata.json")
	if err == nil {
		defer f.Close()
		if json.NewDecoder(io.LimitReader(f, 16<<10)).Decode(&a) != nil || a.ID != id || a.Size < 0 || a.Size > maxFileBytes || len(a.SHA256) != 64 || filepath.Base(a.StorageName) != a.StorageName || a.StorageName == "" {
			return attachment{}, errors.New("附件元数据无效")
		}
		return a, nil
	}
	if !os.IsNotExist(err) {
		return a, errors.New("附件元数据不可用")
	}
	// 第一阶段图片没有元数据，读取时兼容推导，不回填或重写旧文件。
	f, err = openConfined(filepath.Join(s.dataDir, "uploads"), id)
	if err != nil {
		return a, errors.New("附件不存在")
	}
	defer f.Close()
	st, _ := f.Stat()
	if st.Size() > 5<<20 {
		return a, errors.New("旧图片大小无效")
	}
	raw, err := io.ReadAll(f)
	if err != nil || !imageMIME(http.DetectContentType(raw)) {
		return a, errors.New("旧图片类型无效")
	}
	h := sha256.Sum256(raw)
	return attachment{ID: id, Name: "图片", MIME: http.DetectContentType(raw), Size: st.Size(), SHA256: hex.EncodeToString(h[:]), Kind: "image", PreviewKind: "image"}, nil
}

func (s *server) openAttachment(a attachment) (*os.File, error) {
	base, name := filepath.Join(s.dataDir, "files", a.ID), a.StorageName
	if name == "" {
		base, name = filepath.Join(s.dataDir, "uploads"), a.ID
	}
	f, err := openConfined(base, name)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || st.Size() != a.Size {
		f.Close()
		return nil, errors.New("附件大小已变化")
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, io.LimitReader(f, maxFileBytes+1)); err != nil || hex.EncodeToString(hash.Sum(nil)) != a.SHA256 {
		f.Close()
		return nil, errors.New("附件内容校验失败")
	}
	_, err = f.Seek(0, io.SeekStart)
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (s *server) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFileBytes+(1<<20))
	multi, err := r.MultipartReader()
	if err != nil {
		fail(w, 400, "请选择附件")
		return
	}
	part, err := multi.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() == "" {
		fail(w, 400, "请选择一个附件")
		return
	}
	defer part.Close()
	a, err := s.storeAttachment(part, part.FileName(), true)
	if err != nil {
		if strings.Contains(err.Error(), "不能超过") {
			fail(w, 413, err.Error())
		} else {
			fail(w, 500, "保存附件失败，本次未登记")
		}
		return
	}
	jsonOut(w, 201, a.public())
}

func decodePreview(raw []byte, truncated bool) (string, string, error) {
	if bytes.HasPrefix(raw, []byte{0xff, 0xfe}) || bytes.HasPrefix(raw, []byte{0xfe, 0xff}) {
		var order binary.ByteOrder = binary.LittleEndian
		encoding := "UTF-16LE"
		if raw[0] == 0xfe {
			order, encoding = binary.BigEndian, "UTF-16BE"
		}
		raw = raw[2:]
		if len(raw)%2 != 0 {
			return "", "", errors.New("UTF-16 文本编码无效")
		}
		units := make([]uint16, len(raw)/2)
		for i := range units {
			units[i] = order.Uint16(raw[i*2:])
		}
		if truncated && len(units) > 0 && units[len(units)-1] >= 0xd800 && units[len(units)-1] <= 0xdbff {
			units = units[:len(units)-1]
		}
		for i := 0; i < len(units); i++ {
			if units[i] >= 0xd800 && units[i] <= 0xdbff {
				if i+1 == len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
					return "", "", errors.New("UTF-16 文本包含无效的代理对")
				}
				i++
			} else if units[i] >= 0xdc00 && units[i] <= 0xdfff {
				return "", "", errors.New("UTF-16 文本包含无效的代理对")
			}
		}
		return string(utf16.Decode(units)), encoding, nil
	}
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	for trim := 0; trim <= 3 && trim <= len(raw); trim++ {
		candidate := raw[:len(raw)-trim]
		if utf8.Valid(candidate) && !bytes.ContainsRune(candidate, 0) {
			return string(candidate), "UTF-8", nil
		}
		if !truncated {
			break
		}
	}
	return "", "", errors.New("文本不是有效的 UTF-8 或带 BOM 的 UTF-16，请下载后查看")
}

func (s *server) serveAttachment(w http.ResponseWriter, r *http.Request, id, action string) {
	a, err := s.attachment(id)
	if err != nil {
		fail(w, 404, "附件不可用")
		return
	}
	f, err := s.openAttachment(a)
	if err != nil {
		fail(w, 409, "附件内容无法核实，请重新上传或检查原文件")
		return
	}
	defer f.Close()
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if action == "preview" && a.PreviewKind == "text" {
		raw, err := io.ReadAll(io.LimitReader(f, maxTextPreview))
		if err != nil {
			fail(w, 500, "读取预览失败")
			return
		}
		text, encoding, err := decodePreview(raw, a.Size > maxTextPreview)
		if err != nil {
			fail(w, 415, err.Error())
			return
		}
		jsonOut(w, 200, map[string]any{"text": text, "encoding": encoding, "truncated": a.Size > maxTextPreview})
		return
	}
	if action == "download" {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Name}))
	} else {
		if a.PreviewKind != "image" && !(action == "preview" && a.PreviewKind == "pdf") {
			fail(w, 415, "此格式暂不支持预览，请下载查看")
			return
		}
		w.Header().Set("Content-Type", a.MIME)
		w.Header().Set("Content-Disposition", "inline")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'; base-uri 'none'")
	}
	http.ServeContent(w, r, a.Name, a.CreatedAt, f)
}

func clientFingerprint(text string, images, files []string) string {
	raw, _ := json.Marshal(struct {
		Text          string
		Images, Files []string
	}{text, append([]string{}, images...), append([]string{}, files...)})
	return digest(string(raw))
}

func (s *server) submissionInput(id, request, text string, images, files []string, legacy bool) (any, string, string, error) {
	seen, total := map[string]bool{}, int64(0)
	parts, imageTotal := []any{}, int64(0)
	fileNotes := []map[string]any{}
	for _, fileID := range append(append([]string{}, images...), files...) {
		if seen[fileID] {
			return nil, "", "", errors.New("同一个附件不能重复添加")
		}
		seen[fileID] = true
		a, err := s.attachment(fileID)
		if err != nil {
			return nil, "", "", errors.New("附件不可用，请重新上传")
		}
		f, err := s.openAttachment(a)
		if err != nil {
			return nil, "", "", errors.New("附件校验失败，请重新上传")
		}
		total += a.Size
		if total > maxAttachmentBytes {
			f.Close()
			return nil, "", "", errors.New("附件总大小不能超过 100 MiB")
		}
		if len(parts) < len(images) {
			if a.Kind != "image" || a.Size > 5<<20 {
				f.Close()
				return nil, "", "", errors.New("图片格式或大小无效")
			}
			imageTotal += a.Size
			if imageTotal > 6<<20 {
				f.Close()
				return nil, "", "", errors.New("图片总大小不能超过 6 MiB")
			}
			raw, readErr := io.ReadAll(io.LimitReader(f, (5<<20)+1))
			f.Close()
			if readErr != nil {
				return nil, "", "", readErr
			}
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:" + a.MIME + ";base64," + base64.StdEncoding.EncodeToString(raw)}})
		} else {
			f.Close()
			path, _ := filepath.Abs(filepath.Join(s.dataDir, "files", a.ID, a.StorageName))
			if a.StorageName == "" {
				path, _ = filepath.Abs(filepath.Join(s.dataDir, "uploads", a.ID))
			}
			fileNotes = append(fileNotes, map[string]any{"name": a.Name, "path": path, "size": a.Size, "sha256": a.SHA256})
		}
	}
	output, contextNote := "", ""
	if !legacy {
		output, _ = filepath.Abs(filepath.Join(s.dataDir, "outputs", submissionKey(id, request)))
		if err := os.MkdirAll(output, 0700); err != nil {
			return nil, "", "", errors.New("无法创建本次文件输出目录")
		}
		if f, err := openConfined(output, ".probe-never-exists"); err == nil {
			f.Close()
		} else if !os.IsNotExist(err) {
			return nil, "", "", errors.New("文件输出目录不安全")
		}
		notes, _ := json.Marshal(fileNotes)
		quotedOutput, _ := json.Marshal(output)
		token := digest(s.apiKey + "\n" + id + "\n" + request)
		contextNote = fmt.Sprintf("[Haven context %s]\n用户附件清单（JSON）：%s\n附件内容没有内联。请按用户要求使用 read_file 或合适工具读取文件，不要执行附件或原地修改附件；格式不支持时说明原因。没有明确任务时询问用户需求。\n如需生成供用户下载的文件，请只保存到本次目录 %s，保留格式扩展名，并在最终回复中用 [文件名](sandbox:/绝对路径) 明确引用生成的文件。\n[/Haven context %s]", token, notes, quotedOutput, token)
		text += "\n\n" + contextNote
	}
	if len(images) == 0 {
		return text, contextNote, output, nil
	}
	if text != "" {
		parts = append([]any{map[string]any{"type": "text", "text": text}}, parts...)
	}
	return []any{map[string]any{"role": "user", "content": parts}}, contextNote, output, nil
}

var artifactLink = regexp.MustCompile(`!?\[[^\]\r\n]*\]\((?:<([^>\r\n]+)>|([^\s)]+))\)|(?m)^MEDIA:\s*(?:"([^"\r\n]+)"|'([^'\r\n]+)'|([^\r\n]+))`)

func artifactPath(match string) string {
	groups := artifactLink.FindStringSubmatch(match)
	for _, group := range groups[1:] {
		if group != "" {
			path := strings.TrimSpace(strings.TrimPrefix(group, "sandbox:"))
			if decoded, err := url.PathUnescape(path); err == nil {
				path = decoded
			}
			return path
		}
	}
	return ""
}

func (s *server) registeredArtifact(rec submission, path string) (attachment, bool, error) {
	index := digest(rec.SessionID+"\n"+rec.RequestID+"\n"+path) + ".json"
	f, err := openConfined(filepath.Join(s.dataDir, "file-links"), index)
	if os.IsNotExist(err) {
		return attachment{}, false, nil
	}
	if err != nil {
		return attachment{}, false, err
	}
	defer f.Close()
	var binding struct{ ID, Session, Request string }
	if json.NewDecoder(io.LimitReader(f, 16<<10)).Decode(&binding) != nil || binding.Session != rec.SessionID || binding.Request != rec.RequestID {
		return attachment{}, true, errors.New("文件关联损坏")
	}
	a, err := s.attachment(binding.ID)
	return a, true, err
}

func (s *server) captureArtifact(rec submission, message, path string) (attachment, error) {
	s.artifactMu.Lock()
	defer s.artifactMu.Unlock()
	indexDir := filepath.Join(s.dataDir, "file-links")
	if err := os.MkdirAll(indexDir, 0700); err != nil {
		return attachment{}, err
	}
	index := filepath.Join(indexDir, digest(rec.SessionID+"\n"+rec.RequestID+"\n"+path)+".json")
	if a, exists, err := s.registeredArtifact(rec, path); exists || err != nil {
		return a, err
	}
	rel, err := filepath.Rel(rec.OutputDir, path)
	if err != nil {
		return attachment{}, err
	}
	f, err := openConfined(rec.OutputDir, rel)
	if err != nil {
		return attachment{}, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return attachment{}, err
	}
	a, err := s.storeAttachment(f, filepath.Base(path), false)
	if err != nil {
		return attachment{}, err
	}
	after, err := f.Stat()
	if err != nil || before.Size() != a.Size || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return attachment{}, errors.New("生成文件仍在变化，请稍后重试")
	}
	binding := struct{ ID, Session, Request, Message string }{a.ID, rec.SessionID, rec.RequestID, message}
	if err = writeAtomicJSON(index, binding); err != nil {
		return attachment{}, err
	}
	return a, nil
}

func transformContentText(content any, transform func(string) string) any {
	if blocks, ok := contentBlocks(content); ok {
		for _, block := range blocks {
			if block["type"] == "text" {
				if value, ok := block["text"].(string); ok {
					block["text"] = transform(value)
				}
			}
		}
		return blocks
	}
	if value, ok := content.(string); ok {
		return transform(value)
	}
	return content
}

func (s *server) decorateAttachments(ctx context.Context, session string, rows []any) {
	s.submissions.mu.RLock()
	var records []submission
	for _, rec := range s.submissions.records {
		if rec.SessionID == session && rec.Version == 2 && rec.State == "accepted" {
			records = append(records, rec)
		}
	}
	s.submissions.mu.RUnlock()
	terminal := map[string]bool{}
	checked := map[string]bool{}
	for _, item := range rows {
		message, ok := item.(map[string]any)
		if !ok {
			continue
		}
		var attached []any
		seen := map[string]bool{}
		var originalText *string
		transform := func(content string) string {
			if message["role"] == "user" {
				for _, rec := range records {
					if rec.ContextNote == "" || !strings.Contains(content, rec.ContextNote) {
						continue
					}
					content = strings.TrimSpace(strings.Replace(content, rec.ContextNote, "", 1))
					imagesAvailable := len(rec.Images) > 0
					for index, fileID := range append(append([]string{}, rec.Images...), rec.Files...) {
						if a, err := s.attachment(fileID); err == nil {
							attached = append(attached, a.public())
							if index < len(rec.Images) {
								f, err := s.openAttachment(a)
								if err != nil {
									imagesAvailable = false
								} else {
									f.Close()
								}
							}
						} else {
							if index < len(rec.Images) {
								imagesAvailable = false
							}
							message["attachment_notice"] = "部分附件已不可用，原消息仍保留。"
						}
					}
					// 新图片已有可用附件卡片时只展示原正文，避免再次渲染原生图片占位。
					// 元数据或原附件缺失时保留历史图片通道；旧消息没有该标记，不受影响。
					if imagesAvailable {
						value := rec.Text
						originalText = &value
					}
					break
				}
			} else if message["role"] == "assistant" {
				content = artifactLink.ReplaceAllStringFunc(content, func(link string) string {
					path := artifactPath(link)
					if !filepath.IsAbs(path) {
						return link
					}
					for _, rec := range records {
						rel, err := filepath.Rel(rec.OutputDir, path)
						if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
							continue
						}
						a, registered, registrationErr := s.registeredArtifact(rec, path)
						if registrationErr != nil {
							message["attachment_notice"] = "文件关联不可用，请检查服务器附件记录。"
							return "[附件不可用]"
						}
						if !registered {
							if !checked[rec.RunID] {
								checked[rec.RunID] = true
								res, err := s.upstream(ctx, "GET", "/v1/runs/"+url.PathEscape(rec.RunID), nil, nil)
								if err == nil {
									var status struct {
										Status string `json:"status"`
									}
									decodeErr := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&status)
									res.Body.Close()
									terminal[rec.RunID] = res.StatusCode == 200 && decodeErr == nil && (status.Status == "completed" || status.Status == "failed" || status.Status == "cancelled" || status.Status == "interrupted")
								}
							}
							if !terminal[rec.RunID] {
								message["attachment_notice"] = "文件运行状态尚未确认，请稍后刷新。"
								return "[文件待确认]"
							}
							a, err = s.captureArtifact(rec, fmt.Sprint(message["id"]), path)
							if err != nil {
								message["attachment_notice"] = "生成文件不可用、超过 50 MiB 或未通过目录检查。"
								return "[生成文件暂不可下载]"
							}
						}
						if !seen[a.ID] {
							attached = append(attached, a.public())
							seen[a.ID] = true
						}
						return "[" + strings.NewReplacer("[", "", "]", "").Replace(a.Name) + "](/api/uploads/" + a.ID + "/download)"
					}
					if strings.HasPrefix(path, s.dataDir) || strings.HasPrefix(link, "MEDIA:") || strings.Contains(link, "sandbox:") {
						message["attachment_notice"] = "此文件没有可核实的本次输出目录，暂不提供下载。"
						return "[文件未登记]"
					}
					return link
				})
			}
			return content
		}
		message["content"] = transformContentText(message["content"], transform)
		if originalText != nil {
			message["content"] = *originalText
		}
		if len(attached) > 0 {
			message["attachments"] = attached
		}
	}
}
