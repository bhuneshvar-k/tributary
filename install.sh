#!/bin/bash
#
# Tributary CLI Installer
#
# Usage:
#   curl -sSL https://get.tributary.dev | bash
#   curl -sSL https://get.tributary.dev | bash -s -- --version v1.0.0
#   curl -sSL https://get.tributary.dev | bash -s -- --to /custom/path
#
# Options:
#   --version VERSION    Install a specific version (default: latest)
#   --to DIR            Install to a custom directory (default: /usr/local/bin)
#   --help              Show this help message
#
set -e

# Configuration
REPO="bhuneshvar-k/tributary"
BINARY="tributary"
GITHUB_API="https://api.github.com/repos"
GITHUB_RELEASES="https://github.com"

# Default values
VERSION=""
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Helper functions
info() {
    echo -e "${GREEN}✓${NC} $1"
}

warn() {
    echo -e "${YELLOW}⚠${NC} $1"
}

error() {
    echo -e "${RED}✗${NC} $1" >&2
    exit 1
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --version)
            VERSION="$2"
            shift 2
            ;;
        --to)
            INSTALL_DIR="$2"
            shift 2
            ;;
        --help)
            echo "Tributary CLI Installer"
            echo ""
            echo "Usage:"
            echo "  curl -sSL https://get.tributary.dev | bash"
            echo "  curl -sSL https://get.tributary.dev | bash -s -- --version v1.0.0"
            echo "  curl -sSL https://get.tributary.dev | bash -s -- --to /custom/path"
            echo ""
            echo "Options:"
            echo "  --version VERSION    Install a specific version (default: latest)"
            echo "  --to DIR            Install to a custom directory (default: /usr/local/bin)"
            echo "  --help              Show this help message"
            echo ""
            echo "Environment Variables:"
            echo "  INSTALL_DIR         Install directory (default: /usr/local/bin)"
            exit 0
            ;;
        *)
            error "Unknown option: $1. Use --help for usage information."
            ;;
    esac
done

# Detect OS
detect_os() {
    local os
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    case $os in
        darwin)
            echo "darwin"
            ;;
        linux)
            echo "linux"
            ;;
        *)
            error "Unsupported operating system: $os"
            ;;
    esac
}

# Detect Architecture
detect_arch() {
    local arch
    arch=$(uname -m)
    case $arch in
        x86_64|amd64)
            echo "amd64"
            ;;
        aarch64|arm64)
            echo "arm64"
            ;;
        *)
            error "Unsupported architecture: $arch"
            ;;
    esac
}

# Get latest version from GitHub API
get_latest_version() {
    local version
    version=$(curl -sS "${GITHUB_API}/${REPO}/releases/latest" | grep '"tag_name"' | cut -d '"' -f 4)

    if [[ -z "$version" ]]; then
        error "Failed to fetch latest version from GitHub"
    fi

    echo "$version"
}

# Download file with progress
download() {
    local url="$1"
    local output="$2"

    if command -v curl &> /dev/null; then
        curl -sSL --progress-bar -o "$output" "$url"
    elif command -v wget &> /dev/null; then
        wget -q --show-progress -O "$output" "$url"
    else
        error "Neither curl nor wget found. Please install one of them."
    fi
}

# Download and verify checksum
download_with_checksum() {
    local url="$1"
    local output="$2"
    local checksum_url="${url}.sha256"

    # Download the file
    download "$url" "$output"

    # Try to download and verify checksum
    local checksum_file="${output}.sha256"
    if download "$checksum_url" "$checksum_file" 2>/dev/null; then
        local expected_checksum
        expected_checksum=$(cut -d ' ' -f 1 < "$checksum_file")
        local actual_checksum

        if command -v sha256sum &> /dev/null; then
            actual_checksum=$(sha256sum < "$output" | cut -d ' ' -f 1)
        elif command -v shasum &> /dev/null; then
            actual_checksum=$(shasum -a 256 < "$output" | cut -d ' ' -f 1)
        else
            warn "No checksum tool found (sha256sum or shasum). Skipping verification."
            rm -f "$checksum_file"
            return 0
        fi

        if [[ "$expected_checksum" != "$actual_checksum" ]]; then
            rm -f "$output" "$checksum_file"
            error "Checksum verification failed. Expected: $expected_checksum, Got: $actual_checksum"
        fi

        info "Checksum verified"
        rm -f "$checksum_file"
    else
        warn "No checksum file found. Skipping verification."
    fi
}

# Main installation function
main() {
    echo "Installing Tributary CLI..."
    echo ""

    # Detect platform
    local os arch
    os=$(detect_os)
    arch=$(detect_arch)

    info "Detected platform: ${os}/${arch}"

    # Get version
    if [[ -z "$VERSION" ]]; then
        info "Fetching latest version..."
        VERSION=$(get_latest_version)
    fi

    info "Version: ${VERSION}"

    # Construct download URL
    local archive_name="${BINARY}_${VERSION}_${os}_${arch}"
    local download_url="${GITHUB_RELEASES}/${REPO}/releases/download/${VERSION}/${archive_name}.tar.gz"

    info "Download URL: ${download_url}"

    # Create temp directory
    local tmp_dir
    tmp_dir=$(mktemp -d)
    trap "rm -rf $tmp_dir" EXIT

    # Download archive
    info "Downloading..."
    local archive_path="${tmp_dir}/${archive_name}.tar.gz"
    download "$download_url" "$archive_path"

    # Extract archive
    info "Extracting..."
    tar -xzf "$archive_path" -C "$tmp_dir"

    # Check if binary exists
    local binary_path="${tmp_dir}/${BINARY}"
    if [[ ! -f "$binary_path" ]]; then
        error "Binary not found in archive"
    fi

    # Make binary executable
    chmod +x "$binary_path"

    # Create install directory if it doesn't exist
    if [[ ! -d "$INSTALL_DIR" ]]; then
        info "Creating install directory: ${INSTALL_DIR}"
        mkdir -p "$INSTALL_DIR" || error "Failed to create install directory"
    fi

    # Install binary
    info "Installing to ${INSTALL_DIR}/${BINARY}..."

    # Try to install without sudo first
    if cp "$binary_path" "${INSTALL_DIR}/${BINARY}" 2>/dev/null; then
        info "Successfully installed"
    elif command -v sudo &> /dev/null; then
        warn "需要管理员权限，使用 sudo..."
        sudo cp "$binary_path" "${INSTALL_DIR}/${BINARY}"
        info "Successfully installed with sudo"
    else
        error "Failed to install. Try running with sudo or set INSTALL_DIR to a writable directory."
    fi

    echo ""
    echo -e "${GREEN}✓${NC} Tributary ${VERSION} has been installed to ${INSTALL_DIR}/${BINARY}"
    echo ""

    # Check if install directory is in PATH
    if [[ ":$PATH:" != *":${INSTALL_DIR}:"* ]]; then
        warn "Add ${INSTALL_DIR} to your PATH:"
        echo ""
        echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
        echo ""
        echo "Or add it to your shell profile (~/.bashrc, ~/.zshrc, etc.)"
    fi

    echo "Run 'tributary --version' to verify the installation."
}

# Run main function
main
