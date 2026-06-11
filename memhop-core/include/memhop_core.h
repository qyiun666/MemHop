/**
 * MemHop Core — C ABI 接口头文件
 *
 * 6 层仿人脑记忆引擎 SDK
 * 编译产物：libmemhop_core.dylib (macOS) / memhop_core.dll (Windows) / libmemhop_core.so (Linux)
 */

#ifndef MEMHOP_CORE_H
#define MEMHOP_CORE_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/**
 * 错误码
 */
typedef enum {
    MEMHOP_SUCCESS = 0,
    MEMHOP_NOT_INITIALIZED = -1,
    MEMHOP_INVALID_ARGUMENT = -2,
    MEMHOP_STORAGE_ERROR = -3,
    MEMHOP_INTERNAL_ERROR = -4,
} MemHopResult;

/**
 * 初始化 SDK
 *
 * @param model_path 向量模型路径，传 NULL 则仅使用 NgramEncoder
 * @param vector_dim 向量维度，推荐 384 (multilingual-e5-small)
 * @return MemHopResult 错误码
 */
MemHopResult memhop_init(const char* model_path, int vector_dim);

/**
 * 创建 Brain 实例
 *
 * @param brains_dir 数据存储目录
 * @param agent_id Agent 标识符
 * @return MemHopResult 错误码
 */
MemHopResult memhop_create_brain(const char* brains_dir, const char* agent_id);

/**
 * 存储记忆
 *
 * @param text 记忆文本内容
 * @param topic_label 话题标签，可为 NULL
 * @return MemHopResult 错误码
 */
MemHopResult memhop_store(const char* text, const char* topic_label);

/**
 * 检索记忆
 *
 * @param query 查询文本
 * @param max_results 最大返回结果数
 * @param result_buffer 结果缓冲区 (输出)
 * @param result_buffer_size 缓冲区大小 (建议 4096+)
 * @param result_len 实际写入长度 (输出)
 * @return MemHopResult 错误码
 *
 * 结果格式：每行 "score|text\n"
 */
MemHopResult memhop_recall(
    const char* query,
    int max_results,
    char* result_buffer,
    int result_buffer_size,
    int* result_len
);

/**
 * 释放资源
 *
 * @return MemHopResult 错误码
 */
MemHopResult memhop_cleanup(void);

#ifdef __cplusplus
}
#endif

#endif /* MEMHOP_CORE_H */
