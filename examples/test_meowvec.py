#!/usr/bin/env python3
"""
Test client for meowvec gRPC service.
"""

import sys
import os
import grpc
import numpy as np

# Add project root to path
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import vector_model_pb2
import vector_model_pb2_grpc


def test_health_check(stub):
    """Test HealthCheck RPC."""
    print("Testing HealthCheck...")
    response = stub.HealthCheck(vector_model_pb2.HealthCheckRequest())
    print(f"  Healthy: {response.healthy}")
    print(f"  Socket: {response.socket}")
    print(f"  Model: {response.model_name}")
    print(f"  Dimension: {response.dimension}")
    return response


def test_encode(stub, text="Hello, world!"):
    """Test single text encoding."""
    print(f"\nTesting Encode with text: '{text}'")
    response = stub.Encode(vector_model_pb2.EncodeRequest(text=text))
    print(f"  Dimension: {response.dimension}")
    print(f"  Vector length: {len(response.embedding)}")
    if response.embedding:
        print(f"  First 5 values: {response.embedding[:5]}")
    return response


def test_batch_encode(stub, texts=None):
    """Test batch encoding."""
    if texts is None:
        texts = ["Hello, world!", "How are you?", "MemHop is great!"]

    print(f"\nTesting BatchEncode with {len(texts)} texts...")
    response = stub.BatchEncode(
        vector_model_pb2.BatchEncodeRequest(texts=texts)
    )
    print(f"  Number of embeddings: {len(response.embeddings)}")
    for i, emb in enumerate(response.embeddings):
        print(f"  Embedding {i}: dim={len(emb.values)}")
    return response


def cosine_similarity(a, b):
    """Calculate cosine similarity between two vectors."""
    a = np.array(a)
    b = np.array(b)
    return np.dot(a, b) / (np.linalg.norm(a) * np.linalg.norm(b))


def test_similarity(stub):
    """Test that similar texts have high cosine similarity."""
    print("\nTesting similarity...")

    # Encode two similar texts
    text1 = "The cat sat on the mat"
    text2 = "A cat was sitting on a mat"
    text3 = "The stock market crashed today"

    resp1 = stub.Encode(vector_model_pb2.EncodeRequest(text=text1))
    resp2 = stub.Encode(vector_model_pb2.EncodeRequest(text=text2))
    resp3 = stub.Encode(vector_model_pb2.EncodeRequest(text=text3))

    sim_12 = cosine_similarity(resp1.embedding, resp2.embedding)
    sim_13 = cosine_similarity(resp1.embedding, resp3.embedding)

    print(f"  Similarity between '{text1}' and '{text2}': {sim_12:.4f}")
    print(f"  Similarity between '{text1}' and '{text3}': {sim_13:.4f}")

    if sim_12 > 0.8:
        print("  ✓ Similar texts have high similarity (>0.8)")
    else:
        print(f"  ✗ Expected similarity > 0.8, got {sim_12:.4f}")

    if sim_13 < sim_12:
        print("  ✓ Different texts have lower similarity")
    else:
        print(f"  ✗ Expected different texts to have lower similarity")

    return sim_12, sim_13


def main():
    """Run all tests."""
    # Connect to server
    channel = grpc.insecure_channel('127.0.0.1:27110')
    stub = vector_model_pb2_grpc.VectorModelServiceStub(channel)

    try:
        # Test health check
        health = test_health_check(stub)
        if not health.healthy:
            print("ERROR: Server reports unhealthy!")
            return 1

        # Test single encode
        test_encode(stub, "Hello, world!")

        # Test batch encode
        test_batch_encode(stub)

        # Test similarity
        sim_12, sim_13 = test_similarity(stub)

        print("\n" + "="*50)
        print("All tests completed!")
        print("="*50)
        return 0

    except grpc.RpcError as e:
        print(f"\nERROR: gRPC call failed: {e}")
        return 1
    except Exception as e:
        print(f"\nERROR: {e}")
        import traceback
        traceback.print_exc()
        return 1


if __name__ == '__main__':
    sys.exit(main())
