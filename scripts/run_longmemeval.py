#!/usr/bin/env python3
"""
LongMemEval 评估脚本 - 使用向量模型
"""
import subprocess
import sys
import time

def run_longmemeval():
    """运行 LongMemEval 基准测试"""
    print("=" * 60)
    print("LongMemEval 基准测试 - 使用向量模型")
    print("=" * 60)

    # 运行基准测试
    cmd = [
        "cargo", "bench",
        "--features", "candle,bench",
        "--bench", "longmemeval_bench",
        "--", "bench_longmemeval_e2e"
    ]

    print(f"运行命令: {' '.join(cmd)}")
    print("-" * 60)

    start_time = time.time()

    try:
        result = subprocess.run(
            cmd,
            cwd="/Volumes/zt_hd/projects/meow/memhop/memhop-core",
            capture_output=True,
            text=True,
            timeout=300  # 5分钟超时
        )

        elapsed = time.time() - start_time

        print(f"命令执行完成，耗时: {elapsed:.2f}s")
        print("-" * 60)

        if result.returncode == 0:
            print("✓ 基准测试成功完成")
            print("\n输出:")
            print(result.stdout[-2000:] if len(result.stdout) > 2000 else result.stdout)
        else:
            print("✗ 基准测试失败")
            print("\n错误输出:")
            print(result.stderr[-2000:] if len(result.stderr) > 2000 else result.stderr)
            return 1

    except subprocess.TimeoutExpired:
        print("✗ 基准测试超时（超过5分钟）")
        return 1
    except Exception as e:
        print(f"✗ 执行错误: {e}")
        return 1

    return 0

if __name__ == "__main__":
    sys.exit(run_longmemeval())
