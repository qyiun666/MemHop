#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "memhop.h"

int main() {
    // 1. 打开数据库
    void* db = memhop_open(
        "{\"db_path\":\"/tmp/demo.meh\","
        "\"vector_dim\":768,"
        "\"llm\":{\"api_url\":\"https://api.openai.com/v1/chat/completions\","
        "\"api_key\":\"sk-xxxx\",\"model\":\"gpt-4o-mini\"}}"
    );
    if (!db) {
        const char* err = memhop_last_error();
        fprintf(stderr, "memhop_open failed: %s\n", err ? err : "unknown");
        return 1;
    }
    printf("Database opened.\n");

    // 2. 搜索记忆
    const char* search_cmd = "{\"command\":\"search\",\"dialogue\":\"hello world\",\"context_limit\":5}";
    char* result = memhop_execute(db, search_cmd);
    if (result) {
        printf("search result: %s\n", result);
        memhop_free_string(result);
    } else {
        const char* err = memhop_last_error();
        fprintf(stderr, "search failed: %s\n", err ? err : "unknown");
    }

    // 3. 更新记忆（假设 topic_id 来自搜索结果）
    const char* update_cmd = "{"
        "\"command\":\"update\","
        "\"topic_id\":\"0000000000000001\","
        "\"dialogue_text\":\"user: hello\","
        "\"summary\":\"greeting exchange\""
    "}";
    result = memhop_execute(db, update_cmd);
    if (result) {
        printf("update result: %s\n", result);
        memhop_free_string(result);
    } else {
        const char* err = memhop_last_error();
        fprintf(stderr, "update failed: %s\n", err ? err : "unknown");
    }

    // 4. 查询 L2 主题列表
    const char* query_cmd = "{"
        "\"command\":\"query_layer\","
        "\"layer\":\"l2\","
        "\"action\":\"list\","
        "\"list\":{\"page\":1,\"page_size\":10}"
    "}";
    result = memhop_execute(db, query_cmd);
    if (result) {
        printf("query_layer result: %s\n", result);
        memhop_free_string(result);
    } else {
        const char* err = memhop_last_error();
        fprintf(stderr, "query_layer failed: %s\n", err ? err : "unknown");
    }

    // 5. 强制同步到磁盘
    const char* sync_cmd = "{\"command\":\"sync\"}";
    result = memhop_execute(db, sync_cmd);
    if (result) {
        printf("sync result: %s\n", result);
        memhop_free_string(result);
    } else {
        const char* err = memhop_last_error();
        fprintf(stderr, "sync failed: %s\n", err ? err : "unknown");
    }

    // 6. 关闭数据库
    memhop_close(db);
    printf("Database closed.\n");

    return 0;
}
