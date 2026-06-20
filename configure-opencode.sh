#!/usr/bin/env bash
#
# Configure the local opencode setup to use OpenRouter with DeepSeek V4 models.
#
# Writes an opencode.json in this directory that registers the OpenRouter
# provider and the DeepSeek V4 Flash and DeepSeek V4 models. The OpenRouter
# API key is read at runtime from DEEPSEEK_API_KEY, which opencode loads
# automatically from the .env file in this directory.
#
# Usage: ./configure-opencode.sh

set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly ENV_FILE="${SCRIPT_DIR}/.env"
readonly CONFIG_FILE="${SCRIPT_DIR}/opencode.json"
readonly TEMPLATE_FILE="${SCRIPT_DIR}/opencode.template.json"
readonly API_KEY_VAR="DEEPSEEK_API_KEY"

# Default model used when opencode starts. Flash is faster/cheaper; switch to
# openrouter/deepseek/deepseek-v4-pro for the higher-quality model.
readonly DEFAULT_MODEL="openrouter/deepseek/deepseek-v4-flash"

err() {
  echo "Error: $*" >&2
}

# Check that the .env file exists and defines a non-empty OpenRouter API key.
check_api_key() {
  if [[ ! -f "${ENV_FILE}" ]]; then
    err "${ENV_FILE} not found. Create it with a line:"
    err "  ${API_KEY_VAR}=sk-or-v1-..."
    return 1
  fi

  local value
  value="$(grep -E "^${API_KEY_VAR}=" "${ENV_FILE}" | head -n1 | cut -d= -f2- || true)"
  value="${value%\"}"
  value="${value#\"}"

  if [[ -z "${value}" ]]; then
    err "${API_KEY_VAR} is not set in ${ENV_FILE}."
    err "Add your OpenRouter key:  ${API_KEY_VAR}=sk-or-v1-..."
    return 1
  fi

  if [[ "${value}" != sk-or-* ]]; then
    err "${API_KEY_VAR} does not look like an OpenRouter key (expected sk-or-...)."
    err "OpenRouter keys start with 'sk-or-'."
    return 1
  fi
}

# Write opencode.json from the template, substituting the placeholders.
write_config() {
  if [[ ! -f "${TEMPLATE_FILE}" ]]; then
    err "Template ${TEMPLATE_FILE} not found."
    return 1
  fi

  sed \
    -e "s|__API_KEY_VAR__|${API_KEY_VAR}|g" \
    -e "s|__DEFAULT_MODEL__|${DEFAULT_MODEL}|g" \
    "${TEMPLATE_FILE}" > "${CONFIG_FILE}"
}

main() {
  check_api_key
  write_config

  echo "Wrote ${CONFIG_FILE}"
  echo
  echo "opencode is now configured to use OpenRouter via ${API_KEY_VAR} (from .env)."
  echo "Available models:"
  echo "  - openrouter/deepseek/deepseek-v4-flash  (default)"
  echo "  - openrouter/deepseek/deepseek-v4-pro"
  echo
  echo "Run 'opencode' from this directory to use the configuration."
}

main "$@"
