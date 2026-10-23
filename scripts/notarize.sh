#!/bin/sh
# Notarize the release zips in dist/ (or the directory given as $1).
#
# Requires an App Store Connect API key (or Apple ID with an
# app-specific password) stored once in the login keychain:
#
#   xcrun notarytool store-credentials caskvet-notary \
#     --key AuthKey_XXXXXXXXXX.p8 --key-id XXXXXXXXXXXX \
#     --issuer 00000000-0000-0000-0000-000000000000
#
# Run after `goreleaser release`. Notarization is server-side: the zip
# bytes do not change, so no re-upload is needed.

set -eu

PROFILE="caskvet-notary"
DIST="${1:-dist}"

set -- "$DIST"/*.zip
if [ ! -e "$1" ]; then
    echo "no zips found in $DIST" >&2
    exit 1
fi

for zip in "$@"; do
    echo "notarizing $zip ..."
    xcrun notarytool submit "$zip" --keychain-profile "$PROFILE" --wait
done

echo "notarization complete"
