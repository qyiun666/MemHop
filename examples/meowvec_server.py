#!/usr/bin/env python3
"""
Python gRPC vector encoding service using sentence-transformers.

This service implements the VectorModelService defined in proto/vector_model.proto
and loads the local multilingual-e5-small model for encoding.

Usage:
    python3 examples/meowvec_server.py [--port 27110] [--model-path models/multilingual-e5-small/]
"""

import argparse
import logging
import os
import sys
import time
from concurrent import futures

import grpc
from sentence_transformers import SentenceTransformer

# Import generated protobuf stubs
# Add project root to path so we can import the generated stubs
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import vector_model_pb2
import vector_model_pb2_grpc

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger('meowvec_server')

# Default configuration
DEFAULT_PORT = 27110
DEFAULT_MODEL_PATH = 'models/multilingual-e5-small/'
DEFAULT_DIMENSION = 384


class VectorModelServicer(vector_model_pb2_grpc.VectorModelServiceServicer):
    """Implementation of VectorModelService gRPC service."""

    def __init__(self, model_path: str, dimension: int):
        self.model_path = model_path
        self.dimension = dimension
        self.model = None
        self._load_model()

    def _load_model(self):
        """Load the sentence-transformers model."""
        logger.info(f"Loading model from: {self.model_path}")
        start_time = time.time()
        
        try:
            self.model = SentenceTransformer(self.model_path)
            
            # Verify dimension matches
            test_embedding = self.model.encode(["test"], normalize_embeddings=True)
            actual_dim = test_embedding.shape[1]
            
            if actual_dim != self.dimension:
                logger.warning(f"Model dimension mismatch: expected {self.dimension}, got {actual_dim}")
                self.dimension = actual_dim
            
            load_time = time.time() - start_time
            logger.info(f"Model loaded successfully in {load_time:.2f}s (dimension: {self.dimension})")
            
        except Exception as e:
            logger.error(f"Failed to load model: {e}")
            raise

    def Encode(self, request, context):
        """Encode a single text to embedding vector."""
        try:
            text = request.text
            if not text:
                context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
                context.set_error("Text cannot be empty")
                return vector_model_pb2.EncodeResponse()

            # Encode text with normalization for cosine similarity
            embedding = self.model.encode(
                [text],
                normalize_embeddings=True,
                show_progress_bar=False
            )[0]

            # Convert to list of floats
            embedding_list = embedding.astype(float).tolist()

            logger.debug(f"Encoded text (length={len(text)}): dim={len(embedding_list)}")

            return vector_model_pb2.EncodeResponse(
                embedding=embedding_list,
                dimension=self.dimension
            )

        except Exception as e:
            logger.error(f"Encode failed: {e}")
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Encoding failed: {str(e)}")
            return vector_model_pb2.EncodeResponse()

    def BatchEncode(self, request, context):
        """Encode multiple texts to embedding vectors."""
        try:
            texts = list(request.texts)
            if not texts:
                context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
                context.set_error("Texts list cannot be empty")
                return vector_model_pb2.BatchEncodeResponse()

            # Batch encode with normalization
            embeddings = self.model.encode(
                texts,
                normalize_embeddings=True,
                show_progress_bar=False,
                batch_size=min(len(texts), 32)  # Process in batches
            )

            # Convert to list of Embedding messages
            embedding_messages = []
            for emb in embeddings:
                embedding_messages.append(
                    vector_model_pb2.Embedding(
                        values=emb.astype(float).tolist()
                    )
                )

            logger.debug(f"Batch encoded {len(texts)} texts: dim={self.dimension}")

            return vector_model_pb2.BatchEncodeResponse(
                embeddings=embedding_messages
            )

        except Exception as e:
            logger.error(f"BatchEncode failed: {e}")
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Batch encoding failed: {str(e)}")
            return vector_model_pb2.BatchEncodeResponse()

    def HealthCheck(self, request, context):
        """Perform health check and return service status."""
        healthy = self.model is not None
        socket = f"0.0.0.0:{self.port}" if hasattr(self, 'port') else "unknown"
        model_name = os.path.basename(self.model_path.rstrip('/'))

        return vector_model_pb2.HealthCheckResponse(
            healthy=healthy,
            socket=socket,
            model_name=model_name,
            dimension=self.dimension
        )


def serve(port: int, model_path: str, dimension: int):
    """Start the gRPC server."""
    # Create server with thread pool
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=10),
        options=[
            ('grpc.max_send_message_length', 50 * 1024 * 1024),  # 50MB
            ('grpc.max_receive_message_length', 50 * 1024 * 1024),  # 50MB
        ]
    )

    # Initialize service
    servicer = VectorModelServicer(model_path, dimension)
    servicer.port = port

    # Register service
    vector_model_pb2_grpc.add_VectorModelServiceServicer_to_server(
        servicer, server
    )

    # Bind to address
    listen_addr = f'0.0.0.0:{port}'
    server.add_insecure_port(listen_addr)

    # Start server
    server.start()
    logger.info(f"meowvec gRPC server started on {listen_addr}")
    logger.info(f"  Model: {model_path}")
    logger.info(f"  Dimension: {dimension}")
    logger.info("  Press Ctrl+C to stop")

    try:
        # Keep server running
        server.wait_for_termination()
    except KeyboardInterrupt:
        logger.info("Shutting down server...")
        server.stop(grace=5)
        logger.info("Server stopped")


def main():
    parser = argparse.ArgumentParser(
        description='Python gRPC vector encoding service'
    )
    parser.add_argument(
        '--port',
        type=int,
        default=DEFAULT_PORT,
        help=f'Port to listen on (default: {DEFAULT_PORT})'
    )
    parser.add_argument(
        '--model-path',
        type=str,
        default=DEFAULT_MODEL_PATH,
        help=f'Path to sentence-transformers model (default: {DEFAULT_MODEL_PATH})'
    )
    parser.add_argument(
        '--dimension',
        type=int,
        default=DEFAULT_DIMENSION,
        help=f'Expected vector dimension (default: {DEFAULT_DIMENSION})'
    )

    args = parser.parse_args()

    # Resolve model path relative to project root
    project_root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    model_path = args.model_path
    if not os.path.isabs(model_path):
        model_path = os.path.join(project_root, model_path)

    # Verify model path exists
    if not os.path.exists(model_path):
        logger.error(f"Model path does not exist: {model_path}")
        sys.exit(1)

    serve(args.port, model_path, args.dimension)


if __name__ == '__main__':
    main()
