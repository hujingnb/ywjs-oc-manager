#!/usr/bin/env sh
set -eu

openclaw --version
openclaw plugins list | grep -q "openclaw-weixin"
