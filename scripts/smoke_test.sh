#!/bin/sh
set -eu

API_BASE="${API_BASE:-http://localhost:8090}"
TMP_FILE="${TMPDIR:-/tmp}/rag-smoke-upload.txt"

printf 'hello ingestion smoke test\n' > "$TMP_FILE"

echo "Health:"
curl -fsS "$API_BASE/api/health"
echo

echo "Upload:"
curl -fsS -i -F "file=@$TMP_FILE" "$API_BASE/api/ingest"
echo

echo "Jobs:"
curl -fsS "$API_BASE/api/jobs"
echo

echo "Metrics:"
curl -fsS "$API_BASE/api/metrics"
echo
