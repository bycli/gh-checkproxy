#!/bin/sh
# Generate a GPG key for signing the apt repository.
# Run once, then add the output to GitHub repo secrets.
# Requires: gnupg (brew install gnupg on macOS)
set -e

PATH="/usr/local/bin:/opt/homebrew/bin:$PATH"
GNUPGHOME=$(mktemp -d)
export GNUPGHOME
chmod 700 "$GNUPGHOME"

cleanup() {
  rm -rf "$GNUPGHOME"
}
trap cleanup EXIT

echo "Generating GPG key (this may take a moment)..."

gpg --batch --gen-key << 'KEYCONF'
Key-Type: RSA
Key-Length: 4096
Name-Real: gh-checkproxy
Name-Email: bycli@users.noreply.github.com
Expire-Date: 0
%no-protection
%commit
KEYCONF

KEY_ID=$(gpg --list-keys --with-colons | grep '^pub:' | head -1 | cut -d: -f5)
PRIVATE_KEY=$(gpg --armor --export-secret-keys "$KEY_ID")

echo ""
echo "=============================================="
echo "Add this secret to your GitHub repository:"
echo "  Settings → Secrets and variables → Actions"
echo ""
echo "Name:  GPG_PRIVATE_KEY"
echo "Value: (paste the entire block below, including BEGIN and END lines)"
echo "=============================================="
echo ""
echo "$PRIVATE_KEY"
echo ""
echo "=============================================="
echo "Key ID: $KEY_ID"
echo ""
echo "The key was generated in a temporary directory and is not"
echo "in your default keyring. After adding the secret to GitHub,"
echo "you're done. Keep the secret value confidential."
echo "=============================================="
