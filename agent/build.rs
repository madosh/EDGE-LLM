fn main() -> Result<(), Box<dyn std::error::Error>> {
    // The proto is owned by the control plane. Both the repo checkout and the
    // agent Docker build keep it at ../control-plane/proto relative to agent/.
    tonic_build::configure()
        .build_server(false)
        .build_client(true)
        .compile_protos(&["../control-plane/proto/device.proto"], &["../control-plane/proto"])?;
    Ok(())
}
