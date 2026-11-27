// SPDX-License-Identifier: GPL-3.0-or-later
// Juggluco side of gen2diff. Build against Juggluco's own
// libre2_imp/gen2/protocol.{c,h} (GPL-3.0, not vendored here): see run.sh.
//
// Reads one command per line on stdin and answers one line on stdout, "OK
// <hex>" or "ERR <reason>". lt_peer.swift speaks the same protocol, so the
// driver (main.go) can send both the same inputs and compare the answers.
//
//   KEY slot uid                       sensor key
//   BLEREQ slot uid prov chal rnd      19-byte BLE auth request (command 0x1f)
//   BLEOPEN resp                       session key from the 25-byte reply
//   BLEDEC frame                       44 decrypted bytes, under the session key
//   BLEDECK key frame                  the same, under the given key
//   NFCREQ slot uid cmd chal rnd       NFC auth request: the A1 parameters, cmd ‖ 15 bytes
//   NFCDEC key first count cipher      FRAM blocks, nonce from the last NFCREQ
//   CRC data                           CRC-16 as the 2 bytes stored after data
#include "protocol.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static int hexparse(const char *s, uint8_t *out, int max) {
    int n = 0;
    #define HV(c) ((c)>='0'&&(c)<='9'?(c)-'0':((c)|32)>='a'&&((c)|32)<='f'?((c)|32)-'a'+10:-1)
    while (s[0] && s[1] && n < max) {
        int h = HV(s[0]), l = HV(s[1]);
        if (h < 0 || l < 0) return -1;
        out[n++] = (uint8_t)(h << 4 | l);
        s += 2;
    }
    return s[0] ? -1 : n;
}
static void ok(const uint8_t *b, int n) {
    printf("OK ");
    for (int i = 0; i < n; i++) printf("%02x", b[i]);
    printf("\n");
}

static L2Gen2Challenge ble, nfc;
static uint8_t ble_key[L2G2_KEY_BYTES], session_key[L2G2_KEY_BYTES];
static int ble_slot, have_session;

int main(void) {
    static char line[4096], a[5][2048];
    uint8_t uid[64], prov[64], chal[64], rnd[64], key[64], buf[2048], out[2048];
    setvbuf(stdout, NULL, _IOLBF, 0);
    while (fgets(line, sizeof line, stdin)) {
        char cmd[16];
        int n = sscanf(line, "%15s %2047s %2047s %2047s %2047s %2047s", cmd, a[0], a[1], a[2], a[3], a[4]) - 1;
        if (!strcmp(cmd, "KEY") && n == 2) {
            int slot = atoi(a[0]);
            int un = hexparse(a[1], uid, sizeof uid);
            int rc = l2g2_derive_sensor_key(slot, uid, (size_t)un, key);
            if (rc != L2G2_OK) printf("ERR %d\n", rc); else ok(key, L2G2_KEY_BYTES);
        } else if (!strcmp(cmd, "BLEREQ") && n == 5) {
            ble_slot = atoi(a[0]);
            int un = hexparse(a[1], uid, sizeof uid);
            int rc = l2g2_derive_sensor_key(ble_slot, uid, (size_t)un, ble_key);
            if (rc != L2G2_OK) { printf("ERR %d\n", rc); continue; }
            if (hexparse(a[2], prov, sizeof prov) != L2G2_BLE_PROVISIONING_BYTES ||
                hexparse(a[3], chal, sizeof chal) != L2G2_BLE_CHALLENGE_BYTES ||
                hexparse(a[4], rnd, sizeof rnd) != L2G2_RANDOM_BYTES) { printf("ERR args\n"); continue; }
            memset(&ble, 0, sizeof ble);
            have_session = 0;
            l2g2_build_ble_auth_request(&ble, ble_key, 0x1f, prov, chal, rnd, buf);
            ok(buf, L2G2_AUTH_REQUEST_BYTES);
        } else if (!strcmp(cmd, "BLEOPEN") && n == 1) {
            int rn = hexparse(a[0], buf, sizeof buf);
            int rc = l2g2_accept_auth_response(&ble, ble_key, ble_slot, L2G2_BLE, buf, (size_t)rn, key);
            if (rc != L2G2_OK) { printf("ERR %d\n", rc); continue; }
            memcpy(session_key, key, sizeof session_key);
            have_session = 1;
            ok(session_key, L2G2_KEY_BYTES);
        } else if ((!strcmp(cmd, "BLEDEC") && n == 1) || (!strcmp(cmd, "BLEDECK") && n == 2)) {
            const uint8_t *k = session_key;
            const char *frame = a[0];
            if (!strcmp(cmd, "BLEDECK")) {
                if (hexparse(a[0], key, sizeof key) != L2G2_KEY_BYTES) { printf("ERR args\n"); continue; }
                k = key; frame = a[1];
            } else if (!have_session) { printf("ERR no session\n"); continue; }
            int fn = hexparse(frame, buf, sizeof buf);
            if (!l2g2_decrypt_ble_frame(k, ble.stream_prefix, buf, (size_t)fn, out)) { printf("ERR frame\n"); continue; }
            ok(out + 2, fn - 2);
        } else if (!strcmp(cmd, "NFCREQ") && n == 5) {
            int slot = atoi(a[0]);
            int un = hexparse(a[1], uid, sizeof uid);
            int rc = l2g2_derive_sensor_key(slot, uid, (size_t)un, key);
            if (rc != L2G2_OK) { printf("ERR %d\n", rc); continue; }
            uint8_t command;
            if (hexparse(a[2], &command, 1) != 1 ||
                hexparse(a[3], chal, sizeof chal) != L2G2_NFC_CHALLENGE_BYTES ||
                hexparse(a[4], rnd, sizeof rnd) != L2G2_RANDOM_BYTES) { printf("ERR args\n"); continue; }
            memset(&nfc, 0, sizeof nfc);
            if (!l2g2_build_nfc_auth_request(&nfc, key, command, chal, rnd, buf)) { printf("ERR crc\n"); continue; }
            // The NFC request starts with 3 header bytes left zero for the
            // caller; the A1 parameters (command ‖ 15 bytes) follow.
            ok(buf + 3, L2G2_AUTH_REQUEST_BYTES - 3);
        } else if (!strcmp(cmd, "NFCDEC") && n == 4) {
            uint8_t nonce[L2G2_BLOCK_BYTES];
            if (hexparse(a[0], key, sizeof key) != L2G2_KEY_BYTES) { printf("ERR args\n"); continue; }
            int first = atoi(a[1]), count = atoi(a[2]);
            if (hexparse(a[3], buf, sizeof buf) != 8 * count) { printf("ERR args\n"); continue; }
            l2g2_make_data_nonce(&nfc, L2G2_NFC, nonce);
            l2g2_decrypt_nfc_blocks(key, nonce, (uint8_t)first, (uint8_t)count, buf, out);
            ok(out, 8 * count);
        } else if (!strcmp(cmd, "CRC") && n == 1) {
            int dn = hexparse(a[0], buf, sizeof buf);
            uint16_t c = l2g2_crc16(buf, (size_t)dn);
            uint8_t b[2] = { (uint8_t)c, (uint8_t)(c >> 8) };
            ok(b, 2);
        } else {
            printf("ERR unknown\n");
        }
    }
    return 0;
}
