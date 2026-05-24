.PHONY: up up-logs down clean logs gen-proto

# Start the full stack in the background (builds images first)
up:
	docker compose up --build -d

# Start and follow logs
up-logs:
	docker compose up --build

# Stop all containers (keep volumes)
down:
	docker compose down

# Stop and wipe all volumes (certs + data)
clean:
	docker compose down -v --remove-orphans
	rm -f artifacts/sample-gemma-4-e2b.tar.gz artifacts/sample-gemma-4-e2b.tar.gz.sha256

# Tail all logs
logs:
	docker compose logs -f

# Re-run proto generation locally (requires protoc + go plugins)
gen-proto:
	cd control-plane && \
	  protoc \
	    --go_out=. \
	    --go_opt=module=github.com/cami-fleet/control-plane \
	    --go-grpc_out=. \
	    --go-grpc_opt=module=github.com/cami-fleet/control-plane \
	    -I proto \
	    proto/device.proto
	@echo "Generated: control-plane/gen/"

# Quick smoke test: check both devices appear online
smoke:
	@curl -sf -H "X-Api-Key: $${CAMI_API_KEY:-changeme}" \
	  http://localhost:8080/api/devices | \
	  python3 -m json.tool | grep -E '"name"|"status"'
