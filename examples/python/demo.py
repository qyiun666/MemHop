#!/usr/bin/env python3
"""
MemHop Python FFI Demo (ctypes)

Prerequisites:
    - Build libmemhop.dylib (macOS), libmemhop.so (Linux), or memhop.dll (Windows)
    - Place the library in the same directory as this script, or adjust LIB_PATH

Usage:
    python demo.py
"""

import ctypes
import json
import os
import platform

# ---------------------------------------------------------------------------
# 1. 加载动态库（根据平台自动选择）
# ---------------------------------------------------------------------------
system = platform.system()
if system == "Darwin":
    LIB_NAME = "libmemhop.dylib"
elif system == "Linux":
    LIB_NAME = "libmemhop.so"
elif system == "Windows":
    LIB_NAME = "memhop.dll"
else:
    raise RuntimeError(f"Unsupported platform: {system}")

LIB_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", LIB_NAME)
if not os.path.exists(LIB_PATH):
    LIB_PATH = LIB_NAME  # fallback: assume it's in PATH

lib = ctypes.CDLL(LIB_PATH)

# ---------------------------------------------------------------------------
# 2. 定义函数签名
# ---------------------------------------------------------------------------
lib.memhop_open.argtypes = [ctypes.c_char_p]
lib.memhop_open.restype = ctypes.c_void_p

lib.memhop_execute.argtypes = [ctypes.c_void_p, ctypes.c_char_p]
lib.memhop_execute.restype = ctypes.c_char_p

lib.memhop_free_string.argtypes = [ctypes.c_char_p]
lib.memhop_free_string.restype = None

lib.memhop_close.argtypes = [ctypes.c_void_p]
lib.memhop_close.restype = None

lib.memhop_last_error.restype = ctypes.c_char_p


def execute(db, cmd_dict):
    """发送 JSON 命令并返回解析后的 Python 字典。"""
    cmd_json = json.dumps(cmd_dict, ensure_ascii=False).encode("utf-8")
    result_ptr = lib.memhop_execute(db, cmd_json)
    if result_ptr:
        result = ctypes.cast(result_ptr, ctypes.c_char_p).value.decode("utf-8")
        lib.memhop_free_string(result_ptr)
        return json.loads(result)
    else:
        err = lib.memhop_last_error()
        raise RuntimeError(err.decode("utf-8") if err else "unknown error")


def main():
    # -----------------------------------------------------------------------
    # 3. 打开数据库
    # -----------------------------------------------------------------------
    config = {
        "db_path": "/tmp/demo_python.meh",
        "vector_dim": 768,
        "llm": {
            "api_url": "https://api.openai.com/v1/chat/completions",
            "api_key": "sk-xxxx",
            "model": "gpt-4o-mini"
        }
    }

    db = lib.memhop_open(json.dumps(config).encode("utf-8"))
    if not db:
        err = lib.memhop_last_error()
        raise RuntimeError(err.decode("utf-8") if err else "open failed")
    print("[+] Database opened.")

    try:
        # -------------------------------------------------------------------
        # 4. 搜索记忆
        # -------------------------------------------------------------------
        result = execute(db, {
            "command": "search",
            "dialogue": "hello world",
            "context_limit": 5,
            "search_mode": "balanced"
        })
        print("[+] search result:")
        print(json.dumps(result, indent=2, ensure_ascii=False))

        # -------------------------------------------------------------------
        # 5. 更新记忆（假设 topic_id 来自搜索结果）
        # -------------------------------------------------------------------
        result = execute(db, {
            "command": "update",
            "topic_id": "0000000000000001",
            "dialogue_text": "user: hello",
            "summary": "greeting exchange",
            "action_chain": [
                {
                    "title": "respond to greeting",
                    "description": "reply with a friendly greeting",
                    "action_type": "Execute",
                    "parameters": None
                }
            ]
        })
        print("[+] update result:")
        print(json.dumps(result, indent=2, ensure_ascii=False))

        # -------------------------------------------------------------------
        # 6. 查询 L2 主题列表
        # -------------------------------------------------------------------
        result = execute(db, {
            "command": "query_layer",
            "layer": "l2",
            "action": "list",
            "list": {
                "page": 1,
                "page_size": 10,
                "active_only": True
            }
        })
        print("[+] query_layer (l2 list) result:")
        print(json.dumps(result, indent=2, ensure_ascii=False))

        # -------------------------------------------------------------------
        # 7. 强制同步到磁盘
        # -------------------------------------------------------------------
        result = execute(db, {"command": "sync"})
        print("[+] sync result:")
        print(json.dumps(result, indent=2, ensure_ascii=False))

    finally:
        # -------------------------------------------------------------------
        # 8. 关闭数据库
        # -------------------------------------------------------------------
        lib.memhop_close(db)
        print("[+] Database closed.")


if __name__ == "__main__":
    main()
