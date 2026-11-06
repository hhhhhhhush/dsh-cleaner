// Package store 负责读取 DSH 本地数据：会话正文、索引、删除与还原。
package store

import (
	"encoding/json"
	"os"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// Event 会话 jsonl 中的一行事件
type Event struct {
	Type string          `json:"type"`
	Time int64           `json:"time"`
	Data json.RawMessage `json:"data"`
}

// Summary 从会话正文里提取的概要信息
type Summary struct {
	Title     string
	Cwd       string
	CreatedAt int64
	LastTime  int64
	Events    int
}

// 解码器可复用，创建开销较大
var (
	decOnce sync.Once
	decoder *zstd.Decoder
	decErr  error
)

func getDecoder() (*zstd.Decoder, error) {
	decOnce.Do(func() {
		decoder, decErr = zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	})
	return decoder, decErr
}

// Inflate 解压会话文件。
// DSH 的 session.v4.jsonl.zstd 是「多次 append 的多个 zstd frame 拼接」，
// klauspost 的 DecodeAll 原生支持连续帧，无需按 magic 手工切分。
func Inflate(raw []byte) ([]byte, error) {
	dec, err := getDecoder()
	if err != nil {
		return nil, err
	}
	return dec.DecodeAll(raw, nil)
}

// ParseJSONL 逐行解析，残缺行直接跳过（会话文件可能在写入中被截断）
func ParseJSONL(text []byte) []Event {
	var out []Event
	for _, line := range strings.Split(string(text), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e Event
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// ReadEvents 读取一个会话文件的全部事件
func ReadEvents(filePath string) []Event {
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	text, err := Inflate(raw)
	if err != nil {
		return nil
	}
	return ParseJSONL(text)
}

// Summarize 提取标题、工作目录、创建与最后活动时间
func Summarize(filePath string) Summary {
	events := ReadEvents(filePath)
	return summarizeEvents(events)
}

func summarizeEvents(events []Event) Summary {
	var s Summary
	s.Events = len(events)
	fallback := ""
	for _, e := range events {
		switch e.Type {
		case "session":
			var meta struct {
				Cwd       string `json:"cwd"`
				CreatedAt int64  `json:"createdAt"`
			}
			if json.Unmarshal(e.Data, &meta) == nil {
				s.Cwd = meta.Cwd
				s.CreatedAt = meta.CreatedAt
			}
		case "session/title":
			var t struct {
				Title string `json:"title"`
			}
			if json.Unmarshal(e.Data, &t) == nil && t.Title != "" {
				s.Title = t.Title // 取最后一个，与客户端行为一致
			}
		case "user/message":
			if fallback == "" {
				fallback = firstUserText(e.Data)
			}
		}
		if e.Time > s.LastTime {
			s.LastTime = e.Time
		}
	}
	if s.Title == "" {
		s.Title = fallback
	}
	if s.Title == "" {
		// 区分「只有元信息没对话」和「有对话但没有标题」，界面文案更准确
		if s.Events <= 3 {
			s.Title = "（空会话）"
		} else {
			s.Title = "（无标题）"
		}
	}
	return s
}

// firstUserText 取首条用户消息前 40 字，作为无标题会话的兜底
func firstUserText(data json.RawMessage) string {
	var d struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(data, &d) != nil {
		return ""
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(d.Message.Content, &parts) != nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(p.Text)
		sb.WriteString(" ")
	}
	text := strings.TrimSpace(sb.String())
	runes := []rune(text)
	if len(runes) > 40 {
		text = string(runes[:40])
	}
	return text
}
