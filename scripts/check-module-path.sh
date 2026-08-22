#!/usr/bin/env bash
# Chặn module path viết hoa (CQT002). Go coi github.com/CQT002/gokit và
# github.com/cqt002/gokit là HAI module khác nhau — lẫn cả hai vào một build
# graph sinh ra lỗi kiểu "type X is not X".
set -euo pipefail

cd "$(dirname "$0")/.."

# -o để so khớp từng lần xuất hiện, không phải cả dòng: một dòng có thể chứa
# đồng thời bản viết thường (đúng) và bản viết hoa (sai).
hits=$(grep -rInoE 'github\.com/[Cc][Qq][Tt]002' \
	--include='*.go' --include='go.mod' --include='go.work' \
	--include='*.yml' --include='*.yaml' --include='*.md' --include='Makefile' \
	. | grep -v ':github\.com/cqt002$' || true)

if [ -n "$hits" ]; then
	echo "LỖI: module path phải viết thường 'github.com/cqt002/...'. Vi phạm:" >&2
	echo "$hits" >&2
	exit 1
fi

echo "OK: module path viết thường ở mọi nơi."
