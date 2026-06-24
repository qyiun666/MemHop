// Package memhop — Go 调用 libmemhop 的单文件封装
//
// 用法:
//
//	import "yourproject/memhop"
//
//	db, _ := memhop.Open(memhop.Config{DBPath: "./agent.meh", VectorDim: 768})
//	defer db.Close()
//	data, _ := db.Search("hello", "", 10, true)
//
// 编译要求:
//
//	同目录下放对应平台的 libmemhop 动态库，然后:
//	CGO_LDFLAGS="-L. -lmemhop" go build
//
// 运行时:
//
//	macOS:  DYLD_LIBRARY_PATH=. ./yourbinary
//	Linux:  LD_LIBRARY_PATH=. ./yourbinary
//	Win:    把 memhop.dll 和 exe 放同目录
package memhop

/*
#cgo LDFLAGS: -lmemhop
#include <stdlib.h>

void* memhop_open(const char* config_json);
char* memhop_execute(void* handle, const char* command_json);
void  memhop_free_string(char* str);
void  memhop_close(void* handle);
*/
import "C"
import (
	"encoding/json"
	"errors"
	"fmt"
	"unsafe"
)

// ── 类型 ────────────────────────────────────────────────────

// DB 代表一个已打开的 MemHop 数据库连接
type DB struct{ handle unsafe.Pointer }

// Config 打开数据库的配置
type Config struct {
	DBPath          string `json:"db_path"`                     // .meh 文件路径
	VectorDim       int    `json:"vector_dim"`                  // 向量维度，通常 768
	EncoderGrpcAddr string `json:"encoder_grpc_addr,omitempty"` // 可选 gRPC 编码器地址
	CrystalPath     string `json:"crystal_path,omitempty"`      // 可选结晶知识路径
}

// ── 核心 API ────────────────────────────────────────────────

// Open 打开或创建数据库
func Open(cfg Config) (*DB, error) {
	if cfg.VectorDim == 0 {
		cfg.VectorDim = 768
	}
	cfgJSON, _ := json.Marshal(cfg)
	cstr := C.CString(string(cfgJSON))
	defer C.free(unsafe.Pointer(cstr))

	h := C.memhop_open(cstr)
	if h == nil {
		return nil, errors.New("memhop_open failed — check db_path")
	}
	return &DB{handle: h}, nil
}

// Exec 执行任意 JSON 命令，返回 data 字段
// 支持全部 11 种命令: search / update / query_layer / update_title /
// dream / merge_topics / import / session / batch_store / sync / close
func (db *DB) Exec(cmdJSON string) (json.RawMessage, error) {
	if db.handle == nil {
		return nil, errors.New("db closed")
	}
	cstr := C.CString(cmdJSON)
	defer C.free(unsafe.Pointer(cstr))

	p := C.memhop_execute(db.handle, cstr)
	if p == nil {
		return nil, errors.New("exec returned null")
	}
	defer C.memhop_free_string(p)

	var r struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   string          `json:"error"`
	}
	if err := json.Unmarshal([]byte(C.GoString(p)), &r); err != nil {
		return nil, err
	}
	if !r.Success {
		return nil, fmt.Errorf("memhop: %s", r.Error)
	}
	return r.Data, nil
}

// Close 关闭数据库
func (db *DB) Close() error {
	if db.handle == nil {
		return nil
	}
	db.Exec(`{"command":"close"}`)
	C.memhop_close(db.handle)
	db.handle = nil
	return nil
}

// ── 便捷方法 ────────────────────────────────────────────────

// Search 检索记忆
func (db *DB) Search(dialogue string, topicID string, limit int, autoCreate bool) (json.RawMessage, error) {
	ac := 0
	if autoCreate {
		ac = 1
	}
	cid := "null"
	if topicID != "" {
		cid = fmt.Sprintf(`"%s"`, topicID)
	}
	return db.Exec(fmt.Sprintf(
		`{"command":"search","dialogue":%q,"context_id":%s,"context_limit":%d,"auto_create":%d}`,
		dialogue, cid, limit, ac,
	))
}

// Update 写入一轮对话到已激活的 L2 主题
func (db *DB) Update(topicID, dialogue, summary string) (json.RawMessage, error) {
	return db.Exec(fmt.Sprintf(
		`{"command":"update","topic_id":%q,"dialogue_text":%q,"summary":%q,"action_chain":[{"title":"chat","action_type":"Execute"}]}`,
		topicID, dialogue, summary,
	))
}

// ListTopics 列出 L2 主题
func (db *DB) ListTopics(page, pageSize int) (json.RawMessage, error) {
	return db.Exec(fmt.Sprintf(
		`{"command":"query_layer","layer":"l2","action":"list","list":{"page":%d,"page_size":%d}}`,
		page, pageSize,
	))
}

// GetProfile 获取 Agent 画像 (L0)
func (db *DB) GetProfile() (json.RawMessage, error) {
	return db.Exec(`{"command":"query_layer","layer":"l0","action":"get"}`)
}

// Dream 触发记忆整合管线
func (db *DB) Dream(llmCfg map[string]any) (json.RawMessage, error) {
	b, _ := json.Marshal(llmCfg)
	return db.Exec(fmt.Sprintf(`{"command":"dream",%s`, string(b)[1:]))
}

// BatchStore 批量存储文档
func (db *DB) BatchStore(itemsJSON string, sessionID, turnID string) (json.RawMessage, error) {
	return db.Exec(fmt.Sprintf(
		`{"command":"batch_store","items":%s,"session_id":%q,"turn_id":%q}`,
		itemsJSON, sessionID, turnID,
	))
}
