# MemHop FFI SDK v0.25.1

**6层仿人脑记忆引擎 - 跨平台C ABI接口**

---

## 📦 SDK内容

```
sdk-dist/
├── include/
│   └── memhop_core.h              # C头文件(88行)
├── lib/
│   ├── macos/
│   │   ├── x86_64/
│   │   │   └── libmemhop_core.dylib    # Intel Mac (2.9MB)
│   │   ├── aarch64/
│   │   │   └── libmemhop_core.dylib    # Apple Silicon (2.7MB)
│   │   └── libmemhop_core.dylib        # Universal Binary ✅ (17MB)
│   ├── linux/
│   │   └── x86_64/
│   │       └── BUILD-INSTRUCTIONS.md   # Linux编译说明 ⚠️
│   └── windows/
│       └── x86_64/
│           └── BUILD-INSTRUCTIONS.md   # Windows编译说明 ⚠️
└── models/
    └── multilingual-e5-small/          # 向量模型 ✅ (1.3GB)
```

**当前可用**: ✅ macOS (Universal Binary)  
**待编译**: ⚠️ Linux, Windows (见BUILD-INSTRUCTIONS.md)

---

## 🚀 快速开始 (macOS)

### 前置准备

**✅ 模型已包含**: SDK包中已内置multilingual-e5-small模型(1.3GB)

**也可以指定自定义路径**:
```c
// 使用SDK内置模型
memhop_init("./models/multilingual-e5-small", 384);

// 或使用你自己的模型路径
memhop_init("/your/custom/model/path", 384);

// 或不用模型(快速测试)
memhop_init(NULL, 384);  // 使用NgramEncoder
```

### 1. C语言示例

```c
#include <stdio.h>
#include "memhop_core.h"

int main() {
    // 初始化
    // 选项1: 使用模型(需先下载)
    MemHopResult ret = memhop_init("./models/multilingual-e5-small", 384);
    
    // 选项2: 不使用模型(快速测试)
    // MemHopResult ret = memhop_init(NULL, 384);
    if (ret != MEMHOP_SUCCESS) {
        printf("Init failed: %d\n", ret);
        return 1;
    }
    
    // 创建Brain
    ret = memhop_create_brain("./data/agent1", "agent1");
    if (ret != MEMHOP_SUCCESS) {
        printf("Create brain failed: %d\n", ret);
        return 1;
    }
    
    // 存储记忆
    memhop_store("MemHop是脑启发的记忆引擎", "技术");
    memhop_store("支持HNSW向量检索", "技术");
    
    // 检索记忆
    char buffer[4096];
    int result_len = 0;
    ret = memhop_recall("什么是MemHop?", 10, buffer, sizeof(buffer), &result_len);
    
    if (ret == MEMHOP_SUCCESS) {
        printf("Results:\n%s\n", buffer);
    }
    
    // 清理
    memhop_cleanup();
    return 0;
}
```

**编译和运行**:
```bash
gcc -o test test.c \
    -I./include \
    -L./lib/macos \
    -lmemhop_core \
    -Wl,-rpath,@executable_path/../lib/macos

./test
```

### 2. Python示例

```python
import ctypes
import platform

# 加载库
lib = ctypes.CDLL("./lib/macos/libmemhop_core.dylib")

# 设置函数签名
lib.memhop_init.argtypes = [ctypes.c_char_p, ctypes.c_int]
lib.memhop_init.restype = ctypes.c_int

lib.memhop_store.argtypes = [ctypes.c_char_p, ctypes.c_char_p]
lib.memhop_store.restype = ctypes.c_int

lib.memhop_recall.argtypes = [
    ctypes.c_char_p, ctypes.c_int,
    ctypes.c_char_p, ctypes.c_int,
    ctypes.POINTER(ctypes.c_int)
]
lib.memhop_recall.restype = ctypes.c_int

# 使用
lib.memhop_init(b"./models/multilingual-e5-small", 384)
lib.memhop_create_brain(b"./data/agent1", b"agent1")
lib.memhop_store(b"MemHop测试", b"技术")

buffer = ctypes.create_string_buffer(4096)
result_len = ctypes.c_int(0)
lib.memhop_recall(b"测试", 5, buffer, 4096, ctypes.byref(result_len))

print(f"Results:\n{buffer.value[:result_len.value].decode('utf-8')}")
lib.memhop_cleanup()
```

---

## 📋 API参考

### 核心函数

```c
// 初始化SDK
MemHopResult memhop_init(const char* model_path, int vector_dim);

// 创建Brain实例
MemHopResult memhop_create_brain(const char* brains_dir, const char* agent_id);

// 存储记忆
MemHopResult memhop_store(const char* text, const char* topic_label);

// 检索记忆
MemHopResult memhop_recall(
    const char* query,
    int max_results,
    char* result_buffer,
    int result_buffer_size,
    int* result_len
);

// 释放资源
MemHopResult memhop_cleanup(void);
```

### 错误码

```c
typedef enum {
    MEMHOP_SUCCESS = 0,              // 成功
    MEMHOP_NOT_INITIALIZED = -1,     // 未初始化
    MEMHOP_INVALID_ARGUMENT = -2,    // 无效参数
    MEMHOP_STORAGE_ERROR = -3,       // 存储错误
    MEMHOP_INTERNAL_ERROR = -4,      // 内部错误
} MemHopResult;
```

---

## 🔧 构建其他平台

### Linux

见: `lib/linux/BUILD-INSTRUCTIONS.md`

简而言之:
```bash
# 在Linux上
cargo build --release --target x86_64-unknown-linux-gnu
cp target/x86_64-unknown-linux-gnu/release/libmemhop_core.so \
   sdk-dist/lib/linux/x86_64/
```

### Windows

见: `lib/windows/BUILD-INSTRUCTIONS.md`

简而言之:
```powershell
# 在Windows上
cargo build --release --target x86_64-pc-windows-msvc
copy target\x86_64-pc-windows-msvc\release\memhop_core.dll sdk-dist\lib\windows\x86_64\
```

---

## 📊 性能特性

- **检索速度**: O(log N) HNSW近似最近邻搜索
- **内存效率**: LMDB + redb混合存储
- **多语言支持**: 中英文双语编码(multilingual-e5-small)
- **跨平台**: C ABI保证Windows/macOS/Linux兼容

---

## 📚 更多文档

- [FFI跨平台详细指南](../../.qoder/reports/ffi-cross-platform-guide.md)
- [集成指南](../../docs/meowagent-adapter/)
- [GitHub仓库](https://github.com/meow-ai/memhop)

---

## 📝 许可证

ALL RIGHTS RESERVED

---

**版本**: v0.25.1  
**构建日期**: 2026-06-11  
**支持平台**: macOS ✅ | Linux ⚠️ | Windows ⚠️
