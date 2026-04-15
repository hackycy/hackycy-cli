#!/bin/bash
set -euo pipefail

REPO="hackycy/hackycy-cli"
INSTALL_DIR="$HOME/.ycy-cli/bin"
BINARY_NAME="ycy"
CHECKSUMS_FILE="SHA256SUMS"

info() {
  printf "\033[1;34m%s\033[0m\n" "$1"
}

success() {
  printf "\033[1;32m%s\033[0m\n" "$1"
}

error() {
  printf "\033[1;31merror:\033[0m %s\n" "$1" >&2
  exit 1
}

sha256_file() {
  local file_path="${1}"

  if command -v shasum &>/dev/null; then
    shasum -a 256 "$file_path" | awk '{print $1}'
    return
  fi

  if command -v sha256sum &>/dev/null; then
    sha256sum "$file_path" | awk '{print $1}'
    return
  fi

  error "A SHA-256 tool is required but neither shasum nor sha256sum is installed."
}

detect_platform() {
  local os arch

  os=$(uname -s)
  arch=$(uname -m)

  case "$os" in
    Darwin) OS="macos" ;;
    Linux) OS="linux" ;;
    *) error "Unsupported operating system: $os" ;;
  esac

  case "$arch" in
    x86_64 | amd64) ARCH="x64" ;;
    arm64 | aarch64) ARCH="arm64" ;;
    *) error "Unsupported architecture: $arch" ;;
  esac

  ARTIFACT_NAME="${BINARY_NAME}-${OS}-${ARCH}"
  info "Detected platform: ${OS}-${ARCH}"
}

get_latest_version() {
  info "Fetching latest version..."

  if ! command -v curl &>/dev/null; then
    error "curl is required but not installed."
  fi

  RELEASE_JSON=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest")

  VERSION=$(printf '%s' "$RELEASE_JSON" \
    | grep '"tag_name"' \
    | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')

  if [ -z "$VERSION" ]; then
    error "Failed to determine the latest version. Please check your network connection."
  fi

  info "Latest version: ${VERSION}"
}

fetch_expected_hash() {
  local checksums_url="https://github.com/${REPO}/releases/download/${VERSION}/${CHECKSUMS_FILE}"
  local checksums_content=""

  info "Fetching checksums..."

  EXPECTED_HASH=$(printf '%s' "$RELEASE_JSON" \
    | awk -v artifact="$ARTIFACT_NAME" '
      index($0, "\"name\": \"" artifact "\"") {
        found = 1
        next
      }
      found && match($0, /"digest": "sha256:[A-Fa-f0-9]{64}"/) {
        print substr($0, RSTART + 18, 64)
        exit
      }
    ')

  if [ -n "$EXPECTED_HASH" ]; then
    return
  fi

  checksums_content=$(curl -fsSL "$checksums_url")

  EXPECTED_HASH=$(printf '%s\n' "$checksums_content" | awk -v artifact="$ARTIFACT_NAME" '$2 == artifact { print $1 }' | tail -n 1)

  if [ -z "$EXPECTED_HASH" ]; then
    error "Failed to find checksum for ${ARTIFACT_NAME}."
  fi
}

verify_file_hash() {
  local file_path="${1}"
  local actual_hash

  actual_hash=$(sha256_file "$file_path")

  if [ "$actual_hash" != "$EXPECTED_HASH" ]; then
    return 1
  fi
}

verify_binary() {
  local file_path="${1}"
  local expected_version="ycy/${VERSION#v}"
  local actual_version

  if ! actual_version=$("$file_path" --version 2>/dev/null); then
    return 1
  fi

  case "$actual_version" in
    "$expected_version"*) ;;
    *) return 1 ;;
  esac

  if [ -z "$actual_version" ]; then
    return 1
  fi
}

download_binary() {
  local download_url="${1}"
  local target_path="${INSTALL_DIR}/${BINARY_NAME}"
  local temp_path="${target_path}.tmp.$$"
  local backup_path="${target_path}.backup.$$"
  local had_backup=0

  mkdir -p "$INSTALL_DIR"

  info "Downloading ${ARTIFACT_NAME} ${VERSION}..."
  rm -f "$temp_path"
  curl -fSL --progress-bar "$download_url" -o "$temp_path"
  chmod +x "$temp_path"

  # Verify the download
  local file_size
  file_size=$(wc -c < "$temp_path" | tr -d ' ')
  if [ "$file_size" -eq 0 ]; then
    rm -f "$temp_path"
    error "Downloaded file is empty. Please try again."
  fi

  if ! verify_file_hash "$temp_path"; then
    rm -f "$temp_path"
    error "Checksum verification failed for ${ARTIFACT_NAME}."
  fi

  if command -v xattr &>/dev/null; then
    xattr -d com.apple.quarantine "$temp_path" 2>/dev/null || true
  fi

  if [ -f "$target_path" ]; then
    rm -f "$backup_path"
    mv "$target_path" "$backup_path"
    had_backup=1
  fi

  mv "$temp_path" "$target_path"

  if ! verify_file_hash "$target_path"; then
    rm -f "$target_path"
    if [ "$had_backup" -eq 1 ]; then
      mv "$backup_path" "$target_path"
    fi
    error "Installed binary checksum verification failed."
  fi

  if ! verify_binary "$target_path"; then
    rm -f "$target_path"
    if [ "$had_backup" -eq 1 ]; then
      mv "$backup_path" "$target_path"
    fi
    error "Installed binary failed to execute self-check."
  fi

  if [ "$had_backup" -eq 1 ]; then
    rm -f "$backup_path"
  fi
}

setup_path() {
  local profile_file=""
  local path_entry="export PATH=\"\$HOME/.ycy-cli/bin:\$PATH\""

  # Check if already in PATH
  case ":$PATH:" in
    *":$INSTALL_DIR:"*) return ;;
  esac

  # Detect shell and find profile file
  local shell_name
  shell_name=$(basename "${SHELL:-/bin/bash}")

  case "$shell_name" in
    zsh)
      profile_file="$HOME/.zshrc"
      ;;
    bash)
      if [ -f "$HOME/.bashrc" ]; then
        profile_file="$HOME/.bashrc"
      elif [ -f "$HOME/.bash_profile" ]; then
        profile_file="$HOME/.bash_profile"
      else
        profile_file="$HOME/.bashrc"
      fi
      ;;
    fish)
      profile_file="$HOME/.config/fish/config.fish"
      path_entry="fish_add_path $INSTALL_DIR"
      ;;
    *)
      profile_file="$HOME/.profile"
      ;;
  esac

  # Append to profile if not already present
  if [ -n "$profile_file" ]; then
    if [ ! -f "$profile_file" ] || ! grep -q ".ycy-cli/bin" "$profile_file" 2>/dev/null; then
      echo "" >> "$profile_file"
      echo "# ycy cli" >> "$profile_file"
      echo "$path_entry" >> "$profile_file"
      info "Added ${INSTALL_DIR} to PATH in ${profile_file}"
    fi
  fi
}

print_success() {
  echo ""
  success "ycy ${VERSION} has been installed successfully!"
  echo ""
  echo "  Install path: ${INSTALL_DIR}/${BINARY_NAME}"
  echo ""
  echo "  To get started, restart your terminal or run:"
  echo ""

  local shell_name
  shell_name=$(basename "${SHELL:-/bin/bash}")

  case "$shell_name" in
    zsh)  echo "    source ~/.zshrc" ;;
    bash) echo "    source ~/.bashrc" ;;
    fish) echo "    source ~/.config/fish/config.fish" ;;
    *)    echo "    source ~/.profile" ;;
  esac

  echo ""
  echo "  Then run:"
  echo ""
  echo "    ycy --help"
  echo ""
}

main() {
  echo ""
  info "Installing ycy CLI..."
  echo ""

  detect_platform
  get_latest_version
  fetch_expected_hash

  local download_url="https://github.com/${REPO}/releases/download/${VERSION}/${ARTIFACT_NAME}"
  download_binary "$download_url"
  setup_path
  print_success
}

main "$@"
