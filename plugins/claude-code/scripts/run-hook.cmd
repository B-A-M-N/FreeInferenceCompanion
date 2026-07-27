#!/usr/bin/env bash
# FreeInference Companion — generic hook runner for Claude Code and Codex
# This script calls the fi binary and passes stdin through.
set -u

exec fi hook "$@"