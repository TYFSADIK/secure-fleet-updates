#!/bin/sh
# Release signing on the build laptop. The Ed25519 key lives in the YubiKey's OpenPGP applet
# (YubiKey 5, firmware >= 5.2.3) and never leaves the card. Touch is required per signature.
#   gpg --card-edit -> admin -> key-attr -> ECC -> Curve 25519 ; generate
# Usage: ./sign-with-yubikey.sh manifest.unsigned.json  -> manifest.json.sig (detached)
set -eu
gpg --detach-sign --armor --local-user "fleet-release" "$1"
echo "signed $1 -> $1.asc (verify on device with the exported public key)"
# NOTE: ota.Manifest currently uses a raw Ed25519 signature. To use the card directly,
# either export the raw public key from the card and sign the manifest payload with
# `gpg --sign` + strip, or switch Verify to OpenPGP detached signatures (golang.org/x/crypto/openpgp).
