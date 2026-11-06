package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"time"
)

type imageRef struct {
	Session string `json:"s"`
	Message string `json:"m"`
	Index   int    `json:"i"`
	Path    string `json:"p"`
	Expires int64  `json:"e"`
}

func contentBlocks(content any) ([]map[string]any, bool) {
	var blocks []map[string]any
	switch value := content.(type) {
	case string:
		if json.Unmarshal([]byte(value), &blocks) != nil {
			return nil, false
		}
	case []map[string]any:
		blocks = value
	case []any:
		for _, item := range value {
			block, ok := item.(map[string]any)
			if !ok {
				return nil, false
			}
			blocks = append(blocks, block)
		}
	default:
		return nil, false
	}
	return blocks, len(blocks) > 0
}

func (s *server) imageCipher() cipher.AEAD {
	key := sha256.Sum256([]byte("hermes-haven-history-image-v1\n" + s.apiKey))
	block, _ := aes.NewCipher(key[:])
	gcm, _ := cipher.NewGCM(block)
	return gcm
}
func (s *server) imageReference(session, message string, index int, path string) string {
	gcm := s.imageCipher()
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return ""
	}
	raw, _ := json.Marshal(imageRef{session, message, index, path, time.Now().Add(7 * 24 * time.Hour).Unix()})
	// 只有已读取的消息才能签发引用；路径加密后返回，校验身份、消息及允许目录后才读取文件。
	token := base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, raw, nil))
	return fmt.Sprintf("/api/sessions/%s/images/%s/%d?ref=%s", url.PathEscape(session), url.PathEscape(message), index, token)
}
func (s *server) readImageReference(token, session, message string, index int) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	gcm := s.imageCipher()
	if err != nil || len(raw) < gcm.NonceSize() {
		return "", errors.New("引用无效")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	var ref imageRef
	if json.Unmarshal(plain, &ref) != nil || ref.Session != session || ref.Message != message || ref.Index != index || ref.Expires < time.Now().Unix() {
		return "", errors.New("引用不匹配或已过期")
	}
	return ref.Path, nil
}

var uploadID = regexp.MustCompile(`^[a-f0-9]{40}$`)
