// demo — 演示如何用 Go 调用 MemHop
// 把 memhop.go 和本文件放同一个 package 即可直接跑（无需外部依赖）
//
// 编译:
//
//	CGO_LDFLAGS="-L. -lmemhop" go build -o demo
//
// 运行:
//
//	DYLD_LIBRARY_PATH=. ./demo    (macOS)
//	LD_LIBRARY_PATH=. ./demo      (Linux)
package main

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
	"os"
	"unsafe"
)

// ── 精简版 wrapper（和生产代码一致） ─────────────────────────

type DB struct{ h unsafe.Pointer }

func Open(dbPath string, dim int) (*DB, error) {
	cfg, _ := json.Marshal(map[string]any{"db_path": dbPath, "vector_dim": dim})
	cstr := C.CString(string(cfg))
	defer C.free(unsafe.Pointer(cstr))
	p := C.memhop_open(cstr)
	if p == nil {
		return nil, errors.New("open failed")
	}
	return &DB{h: p}, nil
}

func (db *DB) Exec(cmd string) (json.RawMessage, error) {
	cstr := C.CString(cmd)
	defer C.free(unsafe.Pointer(cstr))
	rp := C.memhop_execute(db.h, cstr)
	if rp == nil {
		return nil, errors.New("exec null")
	}
	defer C.memhop_free_string(rp)
	var r struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   string          `json:"error"`
	}
	json.Unmarshal([]byte(C.GoString(rp)), &r)
	if !r.Success {
		return nil, fmt.Errorf("memhop: %s", r.Error)
	}
	return r.Data, nil
}

func (db *DB) Close() {
	db.Exec(`{"command":"close"}`)
	C.memhop_close(db.h)
	db.h = nil
}

// ── demo ────────────────────────────────────────────────────

func main() {
	db, err := Open("./agent.meh", 768)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()

	// 搜记忆（自动创建主题）
	data, _ := db.Exec(`{"command":"search","dialogue":"hello world","auto_create":1}`)
	fmt.Println("search:", string(data))

	// 列出主题
	topics, _ := db.Exec(`{"command":"query_layer","layer":"l2","action":"list","list":{"page":1,"page_size":5}}`)
	fmt.Println("topics:", string(topics))
}
