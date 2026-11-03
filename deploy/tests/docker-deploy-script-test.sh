#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
TEST_ROOT=$(mktemp -d)
trap 'rm -rf "$TEST_ROOT"' EXIT
mkdir -p "$TEST_ROOT/bin" "$TEST_ROOT/work"

# The deployed .env must select the local-directory compose file even when the
# template it starts from does not define COMPOSE_FILE yet.
sed '/^COMPOSE_FILE=/d' "$ROOT_DIR/deploy/.env.example" > "$TEST_ROOT/env.example"
cat > "$TEST_ROOT/compose.local.yml" <<'EOF'
services:
  sub2api:
    image: example
    volumes:
      - ./postgres_data:/var/lib/postgresql/data
EOF

cat > "$TEST_ROOT/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -eu
output=""
url=""
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o) output="$2"; shift ;;
        https://*) url="$1" ;;
    esac
    shift
done
case "$url" in
    */docker-compose.local.yml) cp "$TEST_COMPOSE_FIXTURE" "$output" ;;
    */.env.example) cp "$TEST_ENV_FIXTURE" "$output" ;;
    *) printf 'Unexpected deployment download: %s\n' "$url" >&2; exit 22 ;;
esac
EOF
chmod +x "$TEST_ROOT/bin/curl"

export PATH="$TEST_ROOT/bin:$PATH"
export TEST_COMPOSE_FIXTURE="$TEST_ROOT/compose.local.yml"
export TEST_ENV_FIXTURE="$TEST_ROOT/env.example"

cd "$TEST_ROOT/work"
bash "$ROOT_DIR/deploy/docker-deploy.sh" > "$TEST_ROOT/run.log"

test -f docker-compose.local.yml
test ! -e docker-compose.yml
grep -Fq './postgres_data:/var/lib/postgresql/data' docker-compose.local.yml
grep -Fxq 'COMPOSE_FILE=docker-compose.local.yml' .env
test -d data
test -d postgres_data
test -d redis_data

for key in POSTGRES_PASSWORD JWT_SECRET TOTP_ENCRYPTION_KEY ADMIN_EMAIL; do
    value=$(sed -n "s/^${key}=//p" .env)
    if [ -z "$value" ]; then
        printf 'deployment script did not generate %s\n' "$key" >&2
        exit 1
    fi
done

printf 'docker-deploy script storage selection test passed\n'
