#!/bin/bash
# Qoder 缓存清理脚本
# 使用方法: 关闭 Qoder 后在终端执行 bash ~/cleanup_qoder_cache.sh

QODER_DIR="$HOME/Library/Application Support/Qoder"
SHARED="$QODER_DIR/SharedClientCache"

echo "🧹 开始清理 Qoder 缓存..."

# 1. 清理 workingSpace（模型+索引，6.8GB）
echo "  → 清理 workingSpace (6.8GB)..."
rm -rf "$SHARED/cache/workingSpace/"*

# 2. 清理数据库缓存（952MB）
echo "  → 清理 db cache (952MB)..."
rm -rf "$SHARED/cache/db/"*

# 3. 清理 CLI 缓存（2.3GB）
echo "  → 清理 cli cache (2.3GB)..."
rm -rf "$SHARED/cli/"*

# 4. 清理搜索索引（747MB）
echo "  → 清理 index (747MB)..."
rm -rf "$SHARED/index/"*

# 5. 清理图片缓存
echo "  → 清理 images cache..."
rm -rf "$SHARED/cache/images/"*

# 6. 清理 AI tracker
echo "  → 清理 ai_tracker..."
rm -rf "$SHARED/cache/ai_tracker/"*

# 7. 清理 cli_ws_migration（旧 Quest 数据）
echo "  → 清理 cli_ws_migration (旧Quest)..."
rm -rf "$SHARED/cache/cli_ws_migration/"*

# 8. 清理旧 plans
echo "  → 清理 plans..."
rm -rf "$SHARED/cache/plans/"*

# 9. 清理 resource_snapshot
echo "  → 清理 resource_snapshot..."
rm -rf "$SHARED/cache/resource_snapshot/"*

# 10. 清理 Electron CachedData（637MB）
echo "  → 清理 CachedData (637MB)..."
rm -rf "$QODER_DIR/CachedData/"*

# 11. 清理 CachedExtensionVSIXs（43MB）
echo "  → 清理 CachedExtensionVSIXs (43MB)..."
rm -rf "$QODER_DIR/CachedExtensionVSIXs/"*

# 12. 清理昨天及之前的日志
echo "  → 清理旧日志..."
TODAY=$(date +%Y%m%d)
for log_dir in "$QODER_DIR/logs/"*/; do
    dir_name=$(basename "$log_dir")
    # 提取日期部分 (格式: 20260603T144934)
    log_date="${dir_name%%T*}"
    if [[ "$log_date" < "$TODAY" ]]; then
        rm -rf "$log_dir"
    fi
done

# 13. 清理 GPUCache
echo "  → 清理 GPUCache..."
rm -rf "$QODER_DIR/GPUCache/"*

# 14. 清理 Cache
echo "  → 清理 Cache..."
rm -rf "$QODER_DIR/Cache/"*

echo ""
echo "✅ 清理完成！预计释放约 11.6 GB"
echo "   现在可以重新打开 Qoder 了"
echo "   首次启动会稍慢（需要重建索引和下载模型）"
