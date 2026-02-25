#!/bin/bash
set -euo pipefail

REPO="hackycy-collection/hackycy-cli"
INSTALL_DIR="$HOME/.ycy-cli/bin"
BINARY_NAME="ycy"

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

  VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' \
    | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')

  if [ -z "$VERSION" ]; then
    error "Failed to determine the latest version. Please check your network connection."
  fi

  info "Latest version: ${VERSION}"
}

download_binary() {
  local download_url="${1}"

  mkdir -p "$INSTALL_DIR"

  info "Downloading ${ARTIFACT_NAME} ${VERSION}..."
  curl -fSL --progress-bar "$download_url" -o "${INSTALL_DIR}/${BINARY_NAME}"
  chmod +x "${INSTALL_DIR}/${BINARY_NAME}"

  # Verify the download
  local file_size
  file_size=$(wc -c < "${INSTALL_DIR}/${BINARY_NAME}" | tr -d ' ')
  if [ "$file_size" -eq 0 ]; then
    rm -f "${INSTALL_DIR}/${BINARY_NAME}"
    error "Downloaded file is empty. Please try again."
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

  local download_url="https://github.com/${REPO}/releases/download/${VERSION}/${ARTIFACT_NAME}"
  download_binary "$download_url"
  setup_path
  print_success
}

main "$@"
