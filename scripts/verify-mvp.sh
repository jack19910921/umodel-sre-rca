#!/usr/bin/env bash
set -euo pipefail

: "${SRE_GATEWAY_ADDR:=127.0.0.1:8080}"
: "${SRE_ALIYUN_PROFILE:=sre-ecs-role}"

curl --fail --silent --show-error --max-time 5 http://127.0.0.1/health >/dev/null
systemctl is-active --quiet sre-gateway
aliyun sts GetCallerIdentity --profile "$SRE_ALIYUN_PROFILE" --output json >/dev/null
curl --fail --silent --show-error --max-time 5 "http://${SRE_GATEWAY_ADDR}/healthz" >/dev/null

echo "MVP prerequisite checks passed"
