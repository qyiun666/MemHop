"""
MemHop basic usage examples.

Run:
    python examples/basic_usage.py
"""

import tempfile
import os

import memhop


def example_basic():
    """Basic remember / recall / forget flow."""
    print("=" * 60)
    print("Example 1: Basic remember → recall → forget")
    print("=" * 60)

    with tempfile.TemporaryDirectory() as tmpdir:
        db_path = os.path.join(tmpdir, "demo.db")
        db = memhop.open(db_path)

        # Remember
        m1 = db.remember(
            "今天早上吃了豆浆油条，在楼下老王家",
            meta={"time": "2026-05-19T07:30", "tags": ["早餐", "食物"]},
        )
        print(f"  remembered: {m1}")

        m2 = db.remember(
            "昨天下午开了三小时的架构评审会",
            meta={"time": "2026-05-18T14:00", "tags": ["工作", "会议"]},
        )
        print(f"  remembered: {m2}")

        # Recall
        result = db.recall("今天早上吃了什么")
        print(f"  recall('今天早上吃了什么'): {result}")

        result2 = db.recall("昨天的会")
        print(f"  recall('昨天的会'): {result2}")

        # No match
        result3 = db.recall("火星上有液态水吗")
        print(f"  recall('火星上有液态水吗'): {result3}")

        # Forget
        db.forget(m1)
        result4 = db.recall("豆浆油条")
        print(f"  recall after forget: {result4}")

        db.close()


def example_bulk():
    """Bulk remember and recall among similar memories."""
    print()
    print("=" * 60)
    print("Example 2: Distinguishing similar memories")
    print("=" * 60)

    with tempfile.TemporaryDirectory() as tmpdir:
        db_path = os.path.join(tmpdir, "bulk.db")
        db = memhop.open(db_path)

        foods = [
            "今天吃了豆浆油条",
            "今天吃了包子小米粥",
            "今天吃了三明治咖啡",
            "今天吃了拉面",
        ]

        for f in foods:
            db.remember(f)

        print(f"  stored {len(foods)} food memories")

        # Each recall should find the exact one
        result = db.recall("今天吃了豆浆")
        print(f"  recall('今天吃了豆浆'): {result}")

        result2 = db.recall("咖啡")
        print(f"  recall('咖啡'): {result2}")

        db.close()


def example_stats():
    """Show database stats."""
    print()
    print("=" * 60)
    print("Example 3: Database stats")
    print("=" * 60)

    with tempfile.TemporaryDirectory() as tmpdir:
        db_path = os.path.join(tmpdir, "stats.db")
        db = memhop.open(db_path)

        for i in range(10):
            db.remember(f"这是第 {i+1} 条记忆")

        print(f"  {db.stats}")

        db.close()


if __name__ == "__main__":
    example_basic()
    example_bulk()
    example_stats()
    print()
    print("All examples completed!")
