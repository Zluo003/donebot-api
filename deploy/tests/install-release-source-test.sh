#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
TEST_ROOT=$(mktemp -d)
trap 'rm -rf "$TEST_ROOT"' EXIT
mkdir -p "$TEST_ROOT/bin" "$TEST_ROOT/fixtures" "$TEST_ROOT/archive"
printf '#!/bin/sh\necho DoneBot-test-binary\n' > "$TEST_ROOT/archive/sub2api"
chmod +x "$TEST_ROOT/archive/sub2api"
for arch in amd64 arm64; do
    archive="sub2api_0.3.0_linux_${arch}.tar.gz"
    tar -czf "$TEST_ROOT/fixtures/$archive" -C "$TEST_ROOT/archive" sub2api
    (cd "$TEST_ROOT/fixtures" && sha256sum "$archive") >> "$TEST_ROOT/fixtures/checksums.txt"
done

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
printf '%s\n' "$url" >> "$TEST_CURL_LOG"
case "$url" in
    https://api.github.com/repos/Zluo003/donebot-api/releases/latest)
        printf '{"tag_name":"v0.3.0"}\n' ;;
    https://github.com/Zluo003/donebot-api/releases/download/v0.3.0/*)
        cp "$TEST_FIXTURES/${url##*/}" "$output"
        if [ "${TEST_BAD_CHECKSUM:-}" = 1 ] && [ "${url##*/}" = checksums.txt ]; then
            sed -i 's/^[a-f0-9]*/0000000000000000000000000000000000000000000000000000000000000000/' "$output"
        fi ;;
    *) printf 'Unexpected release source: %s\n' "$url" >&2; exit 22 ;;
esac
EOF
chmod +x "$TEST_ROOT/bin/curl"
export PATH="$TEST_ROOT/bin:$PATH" TEST_FIXTURES="$TEST_ROOT/fixtures"
export TEST_CURL_LOG="$TEST_ROOT/curl.log"
for arch in amd64 arm64; do
    bash -c '
        source <(head -n -1 "$1")
        INSTALL_DIR="$2"
        OS=linux
        ARCH="$3"
        get_latest_version
        download_and_extract
    ' bash "$ROOT_DIR/deploy/install.sh" "$TEST_ROOT/install-$arch" "$arch" > "$TEST_ROOT/$arch.log"
    test "$("$TEST_ROOT/install-$arch/sub2api")" = DoneBot-test-binary
    grep -Fq "https://github.com/Zluo003/donebot-api/releases/download/v0.3.0/sub2api_0.3.0_linux_${arch}.tar.gz" "$TEST_CURL_LOG"
done

if TEST_BAD_CHECKSUM=1 bash -c '
    source <(head -n -1 "$1")
    INSTALL_DIR="$2"
    OS=linux
    ARCH=amd64
    LATEST_VERSION=v0.3.0
    download_and_extract
' bash "$ROOT_DIR/deploy/install.sh" "$TEST_ROOT/rejected" > "$TEST_ROOT/rejected.log" 2>&1; then
    echo "installer accepted a release archive with an invalid checksum" >&2
    exit 1
fi
test ! -e "$TEST_ROOT/rejected/sub2api"
echo "install release source and checksum checks passed"
