#!/bin/bash
# =============================================================================
# DoneBot API Docker Deployment Preparation Script
# =============================================================================
# This script prepares deployment files for DoneBot API:
#   - Downloads docker-compose.local.yml and .env.example
#   - Generates secure secrets (JWT_SECRET, TOTP_ENCRYPTION_KEY, POSTGRES_PASSWORD)
#   - Generates a random admin login email (ADMIN_EMAIL)
#   - Creates necessary data directories
#
# After running this script, you can start services with:
#   docker compose up -d
#
# The generated .env sets COMPOSE_FILE=docker-compose.local.yml so plain
# `docker compose` commands always use the local-directory compose file. This
# avoids accidentally switching to the named-volume docker-compose.yml after
# pulling a repository that tracks both files.
# =============================================================================

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# GitHub raw content base URL
GITHUB_RAW_URL="https://raw.githubusercontent.com/Zluo003/donebot-api/main/deploy"

# Print colored message
print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Generate random secret
generate_secret() {
    openssl rand -hex 32
}

# Generate a random admin login email so new installs never use a guessable default
generate_admin_email() {
    local suffix
    suffix=$(openssl rand -hex 6) || return 1
    [ -n "$suffix" ] || return 1
    echo "admin-${suffix}@sub2api.local"
}

# Check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Main installation function
main() {
    echo ""
    echo "=========================================="
    echo "  DoneBot API Deployment Preparation"
    echo "=========================================="
    echo ""

    # Check if openssl is available
    if ! command_exists openssl; then
        print_error "openssl is not installed. Please install openssl first."
        exit 1
    fi

    # Check if deployment already exists
    if { [ -f "docker-compose.local.yml" ] || [ -f "docker-compose.yml" ]; } && [ -f ".env" ]; then
        print_warning "Deployment files already exist in current directory."
        read -p "Overwrite existing files? (y/N): " -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            print_info "Cancelled."
            exit 0
        fi
    fi

    # Download the local-directory compose file under its own name. Do not save
    # it as docker-compose.yml: repository checkouts also track a named-volume
    # docker-compose.yml, and silently replacing it switches the data location.
    print_info "Downloading docker-compose.local.yml..."
    if command_exists curl; then
        curl -sSL "${GITHUB_RAW_URL}/docker-compose.local.yml" -o docker-compose.local.yml
    elif command_exists wget; then
        wget -q "${GITHUB_RAW_URL}/docker-compose.local.yml" -O docker-compose.local.yml
    else
        print_error "Neither curl nor wget is installed. Please install one of them."
        exit 1
    fi
    print_success "Downloaded docker-compose.local.yml"

    # Download .env.example
    print_info "Downloading .env.example..."
    if command_exists curl; then
        curl -sSL "${GITHUB_RAW_URL}/.env.example" -o .env.example
    else
        wget -q "${GITHUB_RAW_URL}/.env.example" -O .env.example
    fi
    print_success "Downloaded .env.example"

    # Generate .env file with auto-generated secrets
    print_info "Generating secure secrets..."
    echo ""

    # Generate secrets
    JWT_SECRET=$(generate_secret)
    TOTP_ENCRYPTION_KEY=$(generate_secret)
    POSTGRES_PASSWORD=$(generate_secret)
    ADMIN_EMAIL=$(generate_admin_email)

    # Create .env from .env.example
    cp .env.example .env

    # Update .env with generated secrets (cross-platform compatible)
    if sed --version >/dev/null 2>&1; then
        # GNU sed (Linux)
        sed -i "s/^JWT_SECRET=.*/JWT_SECRET=${JWT_SECRET}/" .env
        sed -i "s/^TOTP_ENCRYPTION_KEY=.*/TOTP_ENCRYPTION_KEY=${TOTP_ENCRYPTION_KEY}/" .env
        sed -i "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=${POSTGRES_PASSWORD}/" .env
        sed -i "s/^ADMIN_EMAIL=.*/ADMIN_EMAIL=${ADMIN_EMAIL}/" .env
    else
        # BSD sed (macOS)
        sed -i '' "s/^JWT_SECRET=.*/JWT_SECRET=${JWT_SECRET}/" .env
        sed -i '' "s/^TOTP_ENCRYPTION_KEY=.*/TOTP_ENCRYPTION_KEY=${TOTP_ENCRYPTION_KEY}/" .env
        sed -i '' "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=${POSTGRES_PASSWORD}/" .env
        sed -i '' "s/^ADMIN_EMAIL=.*/ADMIN_EMAIL=${ADMIN_EMAIL}/" .env
    fi

    # Pin plain `docker compose up -d` to the bind-mounted compose file. This
    # keeps PostgreSQL, Redis and application data in ./postgres_data, ./data
    # and ./redis_data instead of silently creating empty named volumes.
    if grep -q '^COMPOSE_FILE=' .env; then
        if sed --version >/dev/null 2>&1; then
            sed -i "s|^COMPOSE_FILE=.*|COMPOSE_FILE=docker-compose.local.yml|" .env
        else
            sed -i '' "s|^COMPOSE_FILE=.*|COMPOSE_FILE=docker-compose.local.yml|" .env
        fi
    else
        printf '\n# Use the local-directory Compose file for plain docker compose commands.\nCOMPOSE_FILE=docker-compose.local.yml\n' >> .env
    fi

    # Create data directories
    print_info "Creating data directories..."
    mkdir -p data postgres_data redis_data
    print_success "Created data directories"

    # Set secure permissions for .env file (readable/writable only by owner)
    chmod 600 .env
    echo ""

    # Display completion message
    echo "=========================================="
    echo "  Preparation Complete!"
    echo "=========================================="
    echo ""
    echo "Generated secure credentials:"
    echo "  POSTGRES_PASSWORD:     ${POSTGRES_PASSWORD}"
    echo "  JWT_SECRET:            ${JWT_SECRET}"
    echo "  TOTP_ENCRYPTION_KEY:   ${TOTP_ENCRYPTION_KEY}"
    echo "  ADMIN_EMAIL:           ${ADMIN_EMAIL}  (admin login username)"
    echo ""
    print_warning "These credentials have been saved to .env file."
    print_warning "Please keep them secure and do not share publicly!"
    echo ""
    echo "Directory structure:"
    echo "  docker-compose.local.yml  - Docker Compose configuration (local directories)"
    echo "  .env                      - Environment variables (generated secrets)"
    echo "  .env.example              - Example template (for reference)"
    echo "  data/                     - Application data (will be created on first run)"
    echo "  postgres_data/            - PostgreSQL data"
    echo "  redis_data/               - Redis data"
    echo ""
    echo "Next steps:"
    echo "  1. (Optional) Edit .env to customize configuration"
    echo "  2. Start services:"
    echo "     docker compose up -d"
    echo ""
    echo "  3. View logs:"
    echo "     docker compose logs -f sub2api"
    echo ""
    echo "  4. Access Web UI:"
    echo "     http://localhost:8080"
    echo ""
    print_info "If admin password is not set in .env, it will be auto-generated."
    print_info "Check logs for the generated admin password on first startup."
    echo ""
}

# Run main function
main "$@"
