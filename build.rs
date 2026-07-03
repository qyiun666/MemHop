fn main() -> Result<(), Box<dyn std::error::Error>> {
    #[cfg(feature = "grpc-encoder")]
    tonic_build::compile_protos("proto/vector_model.proto")?;
    Ok(())
}
