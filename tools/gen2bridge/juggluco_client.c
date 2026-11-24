// SPDX-License-Identifier: GPL-3.0-or-later
// Juggluco Gen2 BLE client harness. Build against Juggluco's own
// libre2_imp/gen2/protocol.{c,h} (GPL-3.0, not vendored here): see run.sh.
// The CRC check mirrors protocol.c matches_crc (stored little-endian).: drives the official Juggluco protocol.c against
// a socket-connected "sensor" (libre2sim gen2). The NFC part is mocked: the 8-byte UID,
// the 10-byte BLE provisioning, and the root slot are passed in as hex args.
// Socket protocol (fixed BLE sizes, in order): CLIENT ->0x20; SENSOR ->14 challenge;
// CLIENT ->19 request; SENSOR ->25 response; SENSOR ->46 frame (repeated).
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
static int readn(int fd,uint8_t*b,int n){int g=0;while(g<n){int r=read(fd,b+g,n-g);if(r<=0)return -1;g+=r;}return 0;}
static int writen(int fd,const uint8_t*b,int n){int w=0;while(w<n){int r=write(fd,b+w,n-w);if(r<=0)return -1;w+=r;}return 0;}
static void ph(const char*t,const uint8_t*b,int n){printf("%s",t);for(int i=0;i<n;i++)printf("%02x",b[i]);printf("\n");}

int main(int argc,char**argv){
    if(!l2g2_cipher_self_test()){fprintf(stderr,"Juggluco Speck self-test FAILED\n");return 2;}
    if(argc<6){fprintf(stderr,"usage: %s host port uidHex provisioningHex rootSlot [commandHex]\n",argv[0]);return 2;}
    const char*host=argv[1]; int port=atoi(argv[2]);
    uint8_t uid[8],prov[10]; int uidn=hexparse(argv[3],uid,8); int provn=hexparse(argv[4],prov,10);
    int slot=atoi(argv[5]); uint8_t command=0x1f; if(argc>6){uint8_t c;if(hexparse(argv[6],&c,1)==1)command=c;}
    if(uidn!=8||provn!=10){fprintf(stderr,"need 8-byte uid and 10-byte provisioning (got %d,%d)\n",uidn,provn);return 2;}

    uint8_t sensor_key[16];
    int ks=l2g2_derive_sensor_key(slot,uid,uidn,sensor_key);
    if(ks!=L2G2_OK){fprintf(stderr,"derive_sensor_key failed %d\n",ks);return 2;}
    ph("client sensor_key: ",sensor_key,16);

    int fd=socket(AF_INET,SOCK_STREAM,0); struct sockaddr_in sa={0};
    sa.sin_family=AF_INET; sa.sin_port=htons(port); inet_pton(AF_INET,host,&sa.sin_addr);
    if(connect(fd,(struct sockaddr*)&sa,sizeof sa)<0){perror("connect");return 2;}

    uint8_t kick=0x20; writen(fd,&kick,1);
    uint8_t challenge[14]; if(readn(fd,challenge,14)){fprintf(stderr,"no challenge\n");return 2;} ph("<- challenge: ",challenge,14);

    L2Gen2Challenge ex; memset(&ex,0,sizeof ex);
    uint8_t host_random[7]={1,2,3,4,5,6,7}; // fixed for a deterministic test
    uint8_t request[19];
    l2g2_build_ble_auth_request(&ex,sensor_key,command,prov,challenge,host_random,request);
    ph("-> request: ",request,19); writen(fd,request,19);

    uint8_t resp[25]; if(readn(fd,resp,25)){fprintf(stderr,"no response\n");return 2;} ph("<- response: ",resp,25);
    uint8_t session_key[16];
    int as=l2g2_accept_auth_response(&ex,sensor_key,slot,L2G2_BLE,resp,25,session_key);
    if(as!=L2G2_OK){fprintf(stderr,"accept_auth_response FAILED %d (session not established)\n",as);return 1;}
    ph("client session_key: ",session_key,16);
    printf("HANDSHAKE OK\n");

    int frames=0,ok=0;
    uint8_t frame[46],out[46];
    while(readn(fd,frame,46)==0){
        frames++;
        bool d=l2g2_decrypt_ble_frame(session_key,ex.stream_prefix,frame,46,out);
        uint16_t crc=l2g2_crc16(out+2,42); uint16_t want=out[44]|(out[45]<<8);
        if(d && crc==want){ok++; printf("frame %d: CRC OK plain=",frames); for(int i=2;i<46;i++)printf("%02x",out[i]); printf("\n");}
        else printf("frame %d: CRC FAIL (crc=%04x want=%04x)\n",frames,crc,want);
            }
    printf("RESULT frames=%d crcOK=%d\n",frames,ok);
    return (frames>0 && ok==frames)?0:1;
}
