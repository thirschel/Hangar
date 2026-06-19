#!/usr/bin/env bash

set -e
set -o pipefail

REPO_OWNER="thirschel"
REPO_NAME="Hangar"
PROJECT_NAME="hangar_daemon"
RELEASE_BINARY_NAME="hangar-daemon"
CHECKSUMS_NAME="checksums.txt"
CHECKSUMS_SIGNATURE_NAME="${CHECKSUMS_NAME}.sig"
CHECKSUMS_CERTIFICATE_NAME="${CHECKSUMS_NAME}.pem"
# The issuer check below limits this to GitHub Actions OIDC; this regex scopes
# the signing identity to workflows/refs in the thirschel/Hangar repository.
COSIGN_CERTIFICATE_IDENTITY_REGEXP='^https://github.com/thirschel/Hangar/'
COSIGN_CERTIFICATE_OIDC_ISSUER='https://token.actions.githubusercontent.com'

setup_shell_and_path() {
    BIN_DIR=${BIN_DIR:-$HOME/.local/bin}

    case $SHELL in
        */zsh)
            PROFILE=$HOME/.zshrc
            ;;
        */bash)
            PROFILE=$HOME/.bashrc
            ;;
        */fish)
            PROFILE=$HOME/.config/fish/config.fish
            ;;
        */ash)
            PROFILE=$HOME/.profile
            ;;
        *)
            echo "could not detect shell, manually add ${BIN_DIR} to your PATH."
            exit 1
    esac

    if [[ ":$PATH:" != *":${BIN_DIR}:"* ]]; then
        echo >> "$PROFILE" && echo "export PATH=\"\$PATH:$BIN_DIR\"" >> "$PROFILE"
    fi
}

detect_platform_and_arch() {
    PLATFORM="$(uname | tr '[:upper:]' '[:lower:]')"
    if [[ "$PLATFORM" == mingw*_nt* ]]; then
        PLATFORM="windows"
    fi

    ARCHITECTURE="$(uname -m)"
    if [ "${ARCHITECTURE}" = "x86_64" ]; then
        # Redirect stderr to /dev/null to avoid printing errors if non Rosetta.
        if [ "$(sysctl -n sysctl.proc_translated 2>/dev/null)" = "1" ]; then
            ARCHITECTURE="arm64" # Rosetta.
        else
            ARCHITECTURE="amd64" # Intel.
        fi
    elif [ "${ARCHITECTURE}" = "arm64" ] || [ "${ARCHITECTURE}" = "aarch64" ]; then
        ARCHITECTURE="arm64" # Arm.
    else
        ARCHITECTURE="amd64" # Amd.
    fi

    if [[ "$PLATFORM" == "windows" ]]; then
        ARCHIVE_EXT=".zip"
        EXTENSION=".exe"
    else
        ARCHIVE_EXT=".tar.gz"
        EXTENSION=""
    fi
}

get_latest_version() {
    # Get latest version from GitHub API, including prereleases
    API_RESPONSE=$(curl -sS "https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}/releases")
    if [ $? -ne 0 ]; then
        echo "Failed to connect to GitHub API"
        exit 1
    fi
    
    if echo "$API_RESPONSE" | grep -q "Not Found"; then
        echo "No releases found in the repository"
        exit 1
    fi
    
    # Get the first release (latest) from the array
    LATEST_VERSION=$(echo "$API_RESPONSE" | grep -m1 '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/' | sed 's/^v//')
    if [ -z "$LATEST_VERSION" ]; then
        echo "Failed to parse version from GitHub API response:"
        echo "$API_RESPONSE" | grep -v "upload_url" # Filter out long upload_url line
        exit 1
    fi
    echo "$LATEST_VERSION"
}

download_file() {
    local url=$1
    local output_path=$2
    local description=$3

    echo "Downloading ${description} from ${url}"
    if ! curl -fsSL "$url" -o "$output_path"; then
        echo "Error: Failed to download ${description}"
        echo "URL attempted: ${url}"
        return 1
    fi
}

download_release() {
    local version=$1
    local binary_url=$2
    local archive_name=$3
    local tmp_dir=$4

    if ! download_file "$binary_url" "${tmp_dir}/${archive_name}" "release asset"; then
        echo "Error: Failed to download release asset"
        echo "This could be because:"
        echo "1. The release ${version} doesn't have assets uploaded yet"
        echo "2. The asset for ${PLATFORM}_${ARCHITECTURE} wasn't built"
        echo "3. The asset name format has changed"
        echo ""
        echo "Expected asset name: ${archive_name}"
        echo "URL attempted: ${binary_url}"
        if [ "$version" == "latest" ]; then
            echo ""
            echo "Tip: Try installing a specific version instead of 'latest'"
            echo "Available versions:"
            echo "$API_RESPONSE" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/' | sed 's/^v//'
        fi
        rm -rf "$tmp_dir"
        exit 1
    fi
}

download_and_verify_release_metadata() {
    local release_url=$1
    local archive_name=$2
    local tmp_dir=$3

    if ! download_file "${release_url}/${CHECKSUMS_NAME}" "${tmp_dir}/${CHECKSUMS_NAME}" "${CHECKSUMS_NAME}"; then
        rm -rf "$tmp_dir"
        exit 1
    fi

    if command -v cosign > /dev/null 2>&1; then
        if ! download_file "${release_url}/${CHECKSUMS_SIGNATURE_NAME}" "${tmp_dir}/${CHECKSUMS_SIGNATURE_NAME}" "${CHECKSUMS_SIGNATURE_NAME}"; then
            rm -rf "$tmp_dir"
            exit 1
        fi
        if ! download_file "${release_url}/${CHECKSUMS_CERTIFICATE_NAME}" "${tmp_dir}/${CHECKSUMS_CERTIFICATE_NAME}" "${CHECKSUMS_CERTIFICATE_NAME}"; then
            rm -rf "$tmp_dir"
            exit 1
        fi
        echo "Verifying ${CHECKSUMS_NAME} signature with cosign"
        if ! cosign verify-blob \
            --certificate "${tmp_dir}/${CHECKSUMS_CERTIFICATE_NAME}" \
            --signature "${tmp_dir}/${CHECKSUMS_SIGNATURE_NAME}" \
            --certificate-identity-regexp "$COSIGN_CERTIFICATE_IDENTITY_REGEXP" \
            --certificate-oidc-issuer "$COSIGN_CERTIFICATE_OIDC_ISSUER" \
            "${tmp_dir}/${CHECKSUMS_NAME}"; then
            echo "Error: cosign verification failed; aborting install"
            rm -rf "$tmp_dir"
            exit 1
        fi
    else
        if [ "$SKIP_SIGNATURE_CHECK" != true ]; then
            echo "Error: cosign is not installed, so the release signature cannot be verified."
            echo "Install cosign and retry, or rerun with --skip-signature-check to acknowledge checksum-only verification."
            rm -rf "$tmp_dir"
            exit 1
        fi
        echo "WARNING: cosign is not installed; skipping signature verification because --skip-signature-check was provided." >&2
        echo "WARNING: Continuing with SHA256 verification against unsigned ${CHECKSUMS_NAME} only." >&2
    fi

    verify_archive_checksum "$archive_name" "$tmp_dir"
}

verify_archive_checksum() {
    local archive_name=$1
    local tmp_dir=$2
    local checksums_path="${tmp_dir}/${CHECKSUMS_NAME}"
    local archive_path="${tmp_dir}/${archive_name}"
    local expected_checksum
    local actual_checksum

    expected_checksum=$(awk -v file="$archive_name" '
        {
            name=$2
            sub(/^\*/, "", name)
            sub(/^\.\//, "", name)
            if (name == file) {
                print tolower($1)
                exit
            }
        }
    ' "$checksums_path")

    if [ -z "$expected_checksum" ]; then
        echo "Error: ${CHECKSUMS_NAME} does not contain an entry for ${archive_name}"
        rm -rf "$tmp_dir"
        exit 1
    fi

    if command -v sha256sum > /dev/null 2>&1; then
        actual_checksum=$(sha256sum "$archive_path" | awk '{print tolower($1)}')
    elif command -v shasum > /dev/null 2>&1; then
        actual_checksum=$(shasum -a 256 "$archive_path" | awk '{print tolower($1)}')
    else
        echo "Error: neither sha256sum nor shasum is available to verify ${archive_name}"
        rm -rf "$tmp_dir"
        exit 1
    fi

    if [ "$actual_checksum" != "$expected_checksum" ]; then
        echo "Error: SHA256 mismatch for ${archive_name}"
        echo "Expected: ${expected_checksum}"
        echo "Actual:   ${actual_checksum}"
        rm -rf "$tmp_dir"
        exit 1
    fi

    echo "Verified SHA256 checksum for ${archive_name}"
}

find_release_binary() {
    local tmp_dir=$1
    local extension=$2
    local candidate

    for candidate in "${RELEASE_BINARY_NAME}${extension}" "claude-squad${extension}" "cs${extension}" "hangar${extension}"; do
        if [ -f "${tmp_dir}/${candidate}" ]; then
            printf '%s\n' "${tmp_dir}/${candidate}"
            return 0
        fi
    done

    return 1
}

extract_and_install() {
    local tmp_dir=$1
    local archive_name=$2
    local bin_dir=$3
    local extension=$4

    if [[ "$PLATFORM" == "windows" ]]; then
        if ! unzip -t "${tmp_dir}/${archive_name}" > /dev/null 2>&1; then
            echo "Error: Downloaded file is not a valid zip archive"
            rm -rf "$tmp_dir"
            exit 1
        fi
        ensure unzip "${tmp_dir}/${archive_name}" -d "$tmp_dir"
    else
        if ! tar tzf "${tmp_dir}/${archive_name}" > /dev/null 2>&1; then
            echo "Error: Downloaded file is not a valid tar.gz archive"
            rm -rf "$tmp_dir"
            exit 1
        fi
        ensure tar xzf "${tmp_dir}/${archive_name}" -C "$tmp_dir"
    fi

    if [ ! -d "$bin_dir" ]; then
        mkdir -p "$bin_dir"
    fi

    # Remove existing binary if upgrading
    if [ "$UPGRADE_MODE" = true ] && [ -f "$bin_dir/$INSTALL_NAME${extension}" ]; then
        echo "Removing previous installation from $bin_dir/$INSTALL_NAME${extension}"
        rm -f "$bin_dir/$INSTALL_NAME${extension}"
    fi

    local source_binary
    if ! source_binary=$(find_release_binary "$tmp_dir" "$extension"); then
        echo "Installation failed, archive did not contain ${RELEASE_BINARY_NAME}${extension}, claude-squad${extension}, cs${extension}, or hangar${extension}"
        rm -rf "$tmp_dir"
        exit 1
    fi

    # Install binary with desired name
    mv "$source_binary" "$bin_dir/$INSTALL_NAME${extension}"
    rm -rf "$tmp_dir"

    if [ ! -f "$bin_dir/$INSTALL_NAME${extension}" ]; then
        echo "Installation failed, could not find $bin_dir/$INSTALL_NAME${extension}"
        exit 1
    fi

    chmod +x "$bin_dir/$INSTALL_NAME${extension}"
    
    echo ""
    if [ "$UPGRADE_MODE" = true ]; then
        echo "Successfully upgraded '$INSTALL_NAME' to:"
    else
        echo "Installed as '$INSTALL_NAME':"
    fi
    echo "$("$bin_dir/$INSTALL_NAME${extension}" version)"
}

check_command_exists() {
    if command -v "$INSTALL_NAME" &> /dev/null; then
        EXISTING_PATH=$(which "$INSTALL_NAME")
        echo "Found existing installation of '$INSTALL_NAME' at $EXISTING_PATH"
        echo "Will upgrade to the latest version"
        UPGRADE_MODE=true
    else
        UPGRADE_MODE=false
    fi
}

check_and_install_dependencies() {
    echo "Checking for required dependencies..."
    
    # Check for tmux
    if ! command -v tmux &> /dev/null; then
        echo "tmux is not installed. Installing tmux..."
        
        if [[ "$PLATFORM" == "darwin" ]]; then
            # macOS
            if command -v brew &> /dev/null; then
                ensure brew install tmux
            else
                echo "Homebrew is not installed. Please install Homebrew first to install tmux."
                echo "Visit https://brew.sh for installation instructions."
                exit 1
            fi
        elif [[ "$PLATFORM" == "linux" ]]; then
            # Linux
            if command -v apt-get &> /dev/null; then
                ensure sudo apt-get update
                ensure sudo apt-get install -y tmux
            elif command -v dnf &> /dev/null; then
                ensure sudo dnf install -y tmux
            elif command -v yum &> /dev/null; then
                ensure sudo yum install -y tmux
            elif command -v pacman &> /dev/null; then
                ensure sudo pacman -S --noconfirm tmux
            else
                echo "Could not determine package manager. Please install tmux manually."
                exit 1
            fi
        elif [[ "$PLATFORM" == "windows" ]]; then
            echo "For Windows, please install tmux via WSL or another method."
            exit 1
        fi
        
        echo "tmux installed successfully."
    else
        echo "tmux is already installed."
    fi
    
    # Check for GitHub CLI (gh)
    if ! command -v gh &> /dev/null; then
        echo "GitHub CLI (gh) is not installed. Installing GitHub CLI..."
        
        if [[ "$PLATFORM" == "darwin" ]]; then
            # macOS
            if command -v brew &> /dev/null; then
                ensure brew install gh
            else
                echo "Homebrew is not installed. Please install Homebrew first to install GitHub CLI."
                echo "Visit https://brew.sh for installation instructions."
                exit 1
            fi
        elif [[ "$PLATFORM" == "linux" ]]; then
            # Linux
            if command -v apt-get &> /dev/null; then
                echo "Installing GitHub CLI on Debian/Ubuntu..."
                ensure curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | sudo dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg
                ensure sudo chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg
                ensure echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | sudo tee /etc/apt/sources.list.d/github-cli.list > /dev/null
                ensure sudo apt-get update
                ensure sudo apt-get install -y gh
            elif command -v dnf &> /dev/null; then
                ensure sudo dnf install -y 'dnf-command(config-manager)'
                ensure sudo dnf config-manager --add-repo https://cli.github.com/packages/rpm/gh-cli.repo
                ensure sudo dnf install -y gh
            elif command -v yum &> /dev/null; then
                ensure sudo yum install -y yum-utils
                ensure sudo yum-config-manager --add-repo https://cli.github.com/packages/rpm/gh-cli.repo
                ensure sudo yum install -y gh
            elif command -v pacman &> /dev/null; then
                ensure sudo pacman -S --noconfirm github-cli
            else
                echo "Could not determine package manager. Please install GitHub CLI manually."
                echo "Visit https://github.com/cli/cli#installation for installation instructions."
                exit 1
            fi
        elif [[ "$PLATFORM" == "windows" ]]; then
            echo "For Windows, please install GitHub CLI manually."
            echo "Visit https://github.com/cli/cli#installation for installation instructions."
            exit 1
        fi
        
        echo "GitHub CLI (gh) installed successfully."
    else
        echo "GitHub CLI (gh) is already installed."
    fi
    
    echo "All dependencies are installed."
}

create_tmp_dir() {
    local base_dir="${XDG_CACHE_HOME:-$HOME/.cache}/hangar-install"
    mkdir -p "$base_dir"
    mktemp -d "${base_dir}/release.XXXXXX"
}

main() {
    # Parse command line arguments
    INSTALL_NAME="cs"
    UPGRADE_MODE=false
    SKIP_SIGNATURE_CHECK=false
    
    while [[ $# -gt 0 ]]; do
        case $1 in
            --name)
                INSTALL_NAME="$2"
                shift 2
                ;;
            --skip-signature-check)
                SKIP_SIGNATURE_CHECK=true
                shift
                ;;
            *)
                echo "Unknown option: $1"
                echo "Usage: install.sh [--name <n>] [--skip-signature-check]"
                exit 1
                ;;
        esac
    done

    check_command_exists
    detect_platform_and_arch
    
    check_and_install_dependencies
    
    setup_shell_and_path

    VERSION=${VERSION:-"latest"}
    if [[ "$VERSION" == "latest" ]]; then
        VERSION=$(get_latest_version)
    fi
    VERSION="${VERSION#v}"

    RELEASE_URL="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/download/v${VERSION}"
    ARCHIVE_NAME="${PROJECT_NAME}_${VERSION}_${PLATFORM}_${ARCHITECTURE}${ARCHIVE_EXT}"
    BINARY_URL="${RELEASE_URL}/${ARCHIVE_NAME}"
    TMP_DIR=$(create_tmp_dir)
    
    download_release "$VERSION" "$BINARY_URL" "$ARCHIVE_NAME" "$TMP_DIR"
    download_and_verify_release_metadata "$RELEASE_URL" "$ARCHIVE_NAME" "$TMP_DIR"
    extract_and_install "$TMP_DIR" "$ARCHIVE_NAME" "$BIN_DIR" "$EXTENSION"
}

# Run a command that should never fail. If the command fails execution
# will immediately terminate with an error showing the failing
# command.
ensure() {
    if ! "$@"; then err "command failed: $*"; fi
}

err() {
    echo "$1" >&2
    exit 1
}

main "$@" || exit 1
