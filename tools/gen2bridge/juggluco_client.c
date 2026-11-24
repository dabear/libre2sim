// SPDX-License-Identifier: GPL-3.0-or-later
// Juggluco Gen2 client harness. Build against Juggluco's own
// libre2_imp/gen2/protocol.{c,h} (GPL-3.0, not vendored here): see run.sh.
//
// Drives Juggluco's protocol.c against cmd/gen2sensor over a socket (see its
// doc for the framing):
//
//   juggluco_client host port uidHex provisioningHex rootSlot [frames]
//       BLE only; the NFC results (provisioning, slot) are given.
//   juggluco_client host port uidHex nfc - [frames]
//       first the NFC side as Juggluco's Gen2.java runs it (prepareNfcCommand,
//       readAuthenticatedFram, enableStreaming, activate), learning the root
//       slot and provisioning, then BLE with what NFC gave.
//
// The frame CRC check mirrors protocol.c matches_crc (stored little-endian);
// protocol.c itself leaves the BLE frame CRC to the caller.
#include "protocol.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <arpa/inet.h>

static int hexparse(const char* s, uint8_t* out, int max){
    int n=0; while(*s && n<max){ if(s[0]==' '||s[0]==','){s++;continue;}
        int hi=s[0], lo=s[1]; if(!lo) break;
        #define HV(c) ((c)>='0'&&(c)<='9'?(c)-'0':((c)|32)>='a'&&((c)|32)<='f'?((c)|32)-'a'+10:-1)
        int h=HV(hi),l=HV(lo); if(h<0||l<0){s++;continue;} out[n++]=(h<<4)|l; s+=2; }
    return n;
}
static int fd;
static int readn(uint8_t*b,int n){int g=0;while(g<n){int r=read(fd,b+g,n-g);if(r<=0)return -1;g+=r;}return 0;}
static int writen(const uint8_t*b,int n){int w=0;while(w<n){int r=write(fd,b+w,n-w);if(r<=0)return -1;w+=r;}return 0;}
static void ph(const char*t,const uint8_t*b,int n){printf("%s",t);for(int i=0;i<n;i++)printf("%02x",b[i]);printf("\n");}

// One NFC custom command 0xA1. raw gets the reply as Juggluco's issuenfc sees
// it: the response-flags byte (0) and then CoreNFC's reply. Returns the raw
// length, or -1 on an NFC error.
static int nfc(const uint8_t *params, int n, uint8_t *raw) {
    uint8_t len = (uint8_t)n, hdr[2];
    if (writen(&len,1) || writen(params,n) || readn(hdr,2)) { fprintf(stderr,"socket\n"); exit(2); }
    raw[0] = 0;
    if (hdr[1] && readn(raw+1,hdr[1])) { fprintf(stderr,"socket\n"); exit(2); }
    if (hdr[0]) { printf("NFC a1 %02x: error 0x%02x\n", params[0], hdr[0]); return -1; }
    return 1 + hdr[1];
}

static uint8_t uid[8];
static const uint8_t host_random[7] = {1,2,3,4,5,6,7}; // fixed for a deterministic test

// Gen2.prepareNfcCommand: root slot (A1 22, raw[3..4]), challenge (A1 20),
// the 19-byte request, whose bytes 3.. are the A1 parameters.
static int prepare(uint8_t command, L2Gen2Challenge *ex, uint8_t key[16], uint8_t request[19]) {
    uint8_t raw[300], p = 0x22;
    int n = nfc(&p,1,raw);
    if (n < 5) { printf("NFC root slot failed\n"); return -1; }
    int slot = raw[3] | (raw[4] << 8);
    if (l2g2_derive_sensor_key(slot, uid, 8, key) != L2G2_OK) { printf("NFC derive key failed (slot %d)\n", slot); return -1; }
    p = 0x20;
    n = nfc(&p,1,raw);
    if (n != 1 + L2G2_NFC_CHALLENGE_BYTES) { printf("NFC challenge failed (%d)\n", n); return -1; }
    memset(ex,0,sizeof *ex);
    if (!l2g2_build_nfc_auth_request(ex, key, command, raw+1, host_random, request)) { printf("NFC challenge CRC failed\n"); return -1; }
    request[0]=2; request[1]=0xa1; request[2]=7; request[3]=command;
    return slot;
}

static int crc_le_ok(const uint8_t *region, int size) {
    return l2g2_crc16(region+2, size-2) == (region[0] | (region[1] << 8));
}

static int run_nfc(int *slot_out, uint8_t prov[10]) {
    L2Gen2Challenge ex; uint8_t key[16], request[19], raw[300], data_key[16], nonce[8], fram[344];
    // readAuthenticatedFram (0x1F), then A1 21 reads of 3 blocks with a 9-byte raw header.
    int slot = prepare(0x1f, &ex, key, request); if (slot < 0) return -1;
    int n = nfc(request+3, 16, raw);
    int st = n > 0 ? l2g2_accept_auth_response(&ex, key, slot, L2G2_NFC, raw+1, n-1, data_key) : -1;
    if (st != L2G2_OK) { printf("NFC FRAM authentication failed: %d\n", st); return -1; }
    l2g2_make_data_nonce(&ex, L2G2_NFC, nonce);
    for (int b = 0; b < 43; b += 3) {
        int count = 43 - b < 3 ? 43 - b : 3;
        uint8_t q[3] = {0x21, (uint8_t)b, (uint8_t)(count-1)};
        n = nfc(q, 3, raw);
        if (n < 9 + count*8) { printf("NFC A1 21 %d failed (%d)\n", b, n); return -1; }
        l2g2_decrypt_nfc_blocks(data_key, nonce, (uint8_t)b, (uint8_t)count, raw+9, fram+b*8);
    }
    int crcs = crc_le_ok(fram,24) && crc_le_ok(fram+24,296) && crc_le_ok(fram+320,24);
    ph("NFC fram=", fram, 344);
    printf("NFC framCRC=%s\n", crcs ? "ok" : "FAIL");
    // Gen2.activate (0x1B): a 5-byte reply.
    slot = prepare(0x1b, &ex, key, request); if (slot < 0) return -1;
    n = nfc(request+3, 16, raw);
    uint8_t act[5];
    printf("NFC activate=%s\n", n > 0 && l2g2_read_command_reply(&ex, key, L2G2_NFC, raw+1, n-1, act, 5) ? "ok" : "FAIL");
    // Gen2.enableStreaming (0x1E): 9-byte reply, provisioning from the metadata.
    slot = prepare(0x1e, &ex, key, request); if (slot < 0) return -1;
    n = nfc(request+3, 16, raw);
    uint8_t reply[9], meta[9];
    if (n <= 0 || !l2g2_read_command_reply(&ex, key, L2G2_NFC, raw+1, n-1, reply, 9)) { printf("NFC enable streaming failed\n"); return -1; }
    l2g2_write_metadata(&ex, slot, meta);
    memcpy(prov, meta, 7); memcpy(prov+7, reply+6, 3);
    printf("NFC slot=%d\n", slot);
    ph("NFC address=", reply, 6);
    ph("NFC provisioning=", prov, 10);
    uint8_t end = 0; writen(&end, 1);
    *slot_out = slot;
    if (!crcs) return -1;
    printf("NFC OK\n");
    return 0;
}

int main(int argc,char**argv){
    if(!l2g2_cipher_self_test()){fprintf(stderr,"Juggluco Speck self-test FAILED\n");return 2;}
    if(argc<6){fprintf(stderr,"usage: %s host port uidHex (provisioningHex rootSlot | nfc -) [frames]\n",argv[0]);return 2;}
    const char*host=argv[1]; int port=atoi(argv[2]);
    int useNFC = strcmp(argv[4],"nfc")==0;
    uint8_t prov[10]; int slot=0;
    if(hexparse(argv[3],uid,8)!=8){fprintf(stderr,"need an 8-byte uid\n");return 2;}
    if(!useNFC){ if(hexparse(argv[4],prov,10)!=10){fprintf(stderr,"need 10-byte provisioning\n");return 2;} slot=atoi(argv[5]); }
    const uint8_t command=0x1f; int maxframes=argc>6?atoi(argv[6]):5;

    fd=socket(AF_INET,SOCK_STREAM,0); struct sockaddr_in sa={0};
    sa.sin_family=AF_INET; sa.sin_port=htons(port); inet_pton(AF_INET,host,&sa.sin_addr);
    if(connect(fd,(struct sockaddr*)&sa,sizeof sa)<0){perror("connect");return 2;}

    if(useNFC){ uint8_t n='N'; writen(&n,1); if(run_nfc(&slot,prov)) return 1; }

    uint8_t sensor_key[16];
    int ks=l2g2_derive_sensor_key(slot,uid,8,sensor_key);
    if(ks!=L2G2_OK){fprintf(stderr,"derive_sensor_key failed %d\n",ks);return 2;}
    ph("client sensor_key: ",sensor_key,16);

    uint8_t kick=0x20; writen(&kick,1);
    uint8_t challenge[14]; if(readn(challenge,14)){fprintf(stderr,"no challenge\n");return 2;} ph("<- challenge: ",challenge,14);

    L2Gen2Challenge ex; memset(&ex,0,sizeof ex);
    uint8_t request[19];
    l2g2_build_ble_auth_request(&ex,sensor_key,command,prov,challenge,host_random,request);
    ph("-> request: ",request,19); writen(request,19);

    uint8_t resp[25]; if(readn(resp,25)){fprintf(stderr,"no response\n");return 2;} ph("<- response: ",resp,25);
    uint8_t session_key[16];
    int as=l2g2_accept_auth_response(&ex,sensor_key,slot,L2G2_BLE,resp,25,session_key);
    if(as!=L2G2_OK){fprintf(stderr,"accept_auth_response FAILED %d (session not established)\n",as);return 1;}
    ph("client session_key: ",session_key,16);
    printf("HANDSHAKE OK\n");

    int frames=0,ok=0;
    uint8_t frame[46],out[46];
    while(frames<maxframes && readn(frame,46)==0){
        frames++;
        bool d=l2g2_decrypt_ble_frame(session_key,ex.stream_prefix,frame,46,out);
        uint16_t crc=l2g2_crc16(out+2,42); uint16_t want=out[44]|(out[45]<<8);
        if(d && crc==want){ok++; printf("frame %d: CRC OK plain=",frames); for(int i=2;i<46;i++)printf("%02x",out[i]); printf("\n");}
        else printf("frame %d: CRC FAIL (crc=%04x want=%04x)\n",frames,crc,want);
    }
    printf("RESULT frames=%d crcOK=%d\n",frames,ok);
    return (frames>0 && ok==frames)?0:1;
}
