#!/bin/bash
# meowagent集成MemHop Binary Crate脚本
# 用途: 配置meowagent使用私有registry的binary crate(看不到源码)
# 
# 使用方法:
#   cd /Volumes/zt_hd/projects/meow/memhop
#   ./scripts/integrate-meowagent.sh

set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

echo_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

echo_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# ==================== 检查前置条件 ====================
echo_info "检查前置条件..."

MEOWAGENT_DIR="/Volumes/zt_hd/projects/meow/meowagent"

if [ ! -d "$MEOWAGENT_DIR" ]; then
    echo_error "meowagent目录不存在: ${MEOWAGENT_DIR}"
    exit 1
fi

if [ ! -f "${MEOWAGENT_DIR}/Cargo.toml" ]; then
    echo_error "Cargo.toml不存在"
    exit 1
fi

# 检查Cargo配置
if ! grep -q "memhop-private" ~/.cargo/config.toml 2>/dev/null; then
    echo_error "~/.cargo/config.toml中未配置memhop-private registry"
    echo_info "请先运行: ./scripts/publish-binary-crate.sh"
    exit 1
fi

echo_info "✅ 前置条件检查通过"

# ==================== 备份当前配置 ====================
echo ""
echo_info "备份当前Cargo.toml..."

cp "${MEOWAGENT_DIR}/Cargo.toml" "${MEOWAGENT_DIR}/Cargo.toml.backup.$(date +%Y%m%d%H%M%S)"
echo_info "✅ 备份完成: ${MEOWAGENT_DIR}/Cargo.toml.backup.*"

# ==================== 添加patch配置 ====================
echo ""
echo_info "添加patch配置(使用私有registry)..."

# 检查是否已有patch配置
if grep -q "\[patch.crates-io\]" "${MEOWAGENT_DIR}/Cargo.toml"; then
    echo_warn "检测到已有的[patch.crates-io]配置"
    echo_warn "将追加memhop-core配置,不会覆盖现有配置"
    echo ""
    read -p "是否继续? (y/n): " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo_info "已取消"
        exit 0
    fi
fi

# 添加patch配置
cat >> "${MEOWAGENT_DIR}/Cargo.toml" << 'EOF'

# MemHop Binary Crate (closed-source distribution)
# meowagent开发者无法查看MemHop源码,只能看到.rlib二进制
[patch.crates-io]
memhop-core = { registry = "memhop-private", version = "0.25.1" }
EOF

echo_info "✅ patch配置已添加到 ${MEOWAGENT_DIR}/Cargo.toml"

# ==================== 清理并重新构建 ====================
echo ""
echo_info "清理cargo缓存..."

cd "$MEOWAGENT_DIR"
cargo clean

echo_info "✅ 缓存清理完成"

echo ""
echo_warn "准备重新构建meowagent..."
echo_warn "这将从私有registry下载memhop-core binary crate"
echo ""
read -p "是否立即构建? (y/n): " -n 1 -r
echo

if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo_info "开始构建..."
    cargo build --release
    
    if [ $? -eq 0 ]; then
        echo_info "✅ 构建成功!"
    else
        echo_error "构建失败"
        echo_warn "可能的原因:"
        echo_warn "1. memhop-core未发布到私有registry"
        echo_warn "2. Cargo token配置错误"
        echo_warn "3. 网络连接问题"
        exit 1
    fi
else
    echo_info "跳过构建,你可以稍后手动执行: cargo build --release"
fi

# ==================== 验证看不到源码 ====================
echo ""
echo_info "验证meowagent无法查看MemHop源码..."

# 查找memhop-core源码目录
SRC_DIRS=$(find ~/.cargo/registry/src -name "memhop-core-*" -type d 2>/dev/null || true)

if [ -z "$SRC_DIRS" ]; then
    echo_info "✅ 验证通过: 没有memhop-core源码目录"
    echo_info "   meowagent开发者无法查看MemHop源代码"
else
    echo_error "❌ 验证失败: 发现memhop-core源码目录"
    echo_error "   ${SRC_DIRS}"
    echo_warn "   这可能是从path依赖或其他来源获取的"
    echo_warn "   请确认Cargo.toml中的patch配置是否正确"
fi

# 检查.crate文件
CRATE_FILES=$(find ~/.cargo/registry/cache -name "memhop-core-*.crate" 2>/dev/null || true)

if [ -n "$CRATE_FILES" ]; then
    echo_info "✅ 发现binary crate文件:"
    echo "$CRATE_FILES" | while read -r file; do
        SIZE=$(du -h "$file" | cut -f1)
        echo "   - $(basename $file) (${SIZE})"
    done
else
    echo_warn "⚠️ 未发现.crate文件(可能还未下载)"
fi

# ==================== 生成报告 ====================
echo ""
echo_info "=========================================="
echo_info "🎉 meowagent集成完成!"
echo_info "=========================================="
echo ""
echo_info "当前配置:"
echo "   - memhop-core来源: 私有registry (memhop-private)"
echo "   - 版本: 0.25.1"
echo "   - 源码可见性: ❌ 不可见(binary crate)"
echo ""
echo_info "验证命令:"
echo ""
echo_info "1. 检查依赖来源:"
echo "   cd ${MEOWAGENT_DIR}"
echo "   cargo tree -p memhop-core"
echo "   # 应显示: memhop-core v0.25.1 (registry+https://npm.pkg.github.com/...)"
echo ""
echo_info "2. 确认无源码:"
echo "   find ~/.cargo/registry/src -name 'memhop-core-*'"
echo "   # 应该没有输出"
echo ""
echo_info "3. 查看binary crate:"
echo "   ls -lh ~/.cargo/registry/cache/*/memhop-core-*.crate"
echo ""
echo_warn "⚠️ 重要提示:"
echo_warn "- meowagent开发者只能看到.rlib二进制,无法查看MemHop源码"
echo_warn "- 如需切换回本地开发模式,删除Cargo.toml末尾的[patch]段落"
echo_warn "- 或恢复备份: cp Cargo.toml.backup.* Cargo.toml"
echo ""
echo_info "详细文档: /Volumes/zt_hd/projects/meow/memhop/docs/GITHUB-PACKAGES-SETUP.md"
echo ""
