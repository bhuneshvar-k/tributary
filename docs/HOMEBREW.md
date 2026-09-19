# Homebrew Installation

Tributary is available via Homebrew for macOS and Linux users.

## Installation

```sh
# Add the tap
brew tap bhuneshvar-k/tap

# Install tributary
brew install tributary
```

Or in one command:

```sh
brew install bhuneshvar-k/tap/tributary
```

## Upgrading

```sh
brew upgrade tributary
```

## Uninstalling

```sh
brew uninstall tributary
brew untap bhuneshvar-k/tap
```

## How It Works

The Homebrew tap is automatically updated when a new version is released on GitHub. GoReleaser handles:

1. Building binaries for all platforms
2. Creating the GitHub Release
3. Updating the Homebrew cask formula in the tap repository

### Tap Repository

The tap repository is located at: [bhuneshvar-k/homebrew-tap](https://github.com/bhuneshvar-k/homebrew-tap)

### Manual Installation

If you prefer not to use Homebrew, you can install using:

- **curl installer**: `curl -sSL https://get.tributary.dev | bash`
- **GitHub Releases**: Download from [Releases](https://github.com/bhuneshvar-k/tributary/releases)
- **Go install**: `go install github.com/bhuneshvar-k/tributary/cmd/tributary@latest`

## Troubleshooting

### Command not found

If `tributary` is not found after installation, add Homebrew's bin directory to your PATH:

```sh
# For Apple Silicon Macs
echo 'eval "$(/opt/homebrew/bin/brew shellenv)"' >> ~/.zshrc
source ~/.zshrc

# For Intel Macs
echo 'eval "$(/usr/local/bin/brew shellenv)"' >> ~/.zshrc
source ~/.zshrc
```

### Permission errors

If you encounter permission errors:

```sh
sudo brew install bhuneshvar-k/tap/tributary
```

Or better, fix Homebrew permissions:

```sh
sudo chown -R $(whoami) /opt/homebrew
```
