fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Proto lives one level up (mounted at /build/proto in Docker)
    tonic_build::configure()
        .build_server(false)
        .build_client(true)
        .compile_protos(&["../proto/device.proto"], &["../proto"])?;
    Ok(())
}
