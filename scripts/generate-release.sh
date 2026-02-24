#!/bin/sh
# Generate Release file for apt repository.
# The workflow signs it with GPG (InRelease, Release.gpg) before publishing.
set -e

do_hash() {
    HASH_NAME=$1
    HASH_CMD=$2
    echo "${HASH_NAME}:"
    for f in $(find . -type f | sort); do
        f=$(echo "$f" | cut -c3-) # remove ./ prefix
        if [ "$f" = "Release" ]; then
            continue
        fi
        echo " $(${HASH_CMD} "${f}" | cut -d" " -f1) $(wc -c < "${f}") ${f}"
    done
}

cat << EOF
Origin: gh-checkproxy
Label: gh-checkproxy
Suite: stable
Codename: stable
Architectures: amd64 arm64
Components: main
Description: GitHub Checks API proxy for fine-grained personal access tokens
Date: $(date -Ru)
EOF
do_hash "MD5Sum" "md5sum"
do_hash "SHA1" "sha1sum"
do_hash "SHA256" "sha256sum"
