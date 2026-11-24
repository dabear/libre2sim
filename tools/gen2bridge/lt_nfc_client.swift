// SPDX-License-Identifier: GPL-3.0-or-later
//
// LibreTransmitter Gen2 NFC + BLE client harness. It runs LibreTransmitter's own
// `Libre2Gen2Pairing.pair` (from SensorPairingService+Gen2.swift, the part that
// does not touch CoreNFC; run.sh extracts it) with gen2sensor's NFC channel as
// the tag, then logs in over BLE with what pairing returned, using
// LibreTransmitter's `Libre2Gen2Session`. Output matches juggluco_client.c's
// NFC mode so run.sh compares both the same way.
//
// `SensorData` is stubbed (run.sh compares the FRAM byte for byte with the
// sim's instead); LibreTransmitter's real SensorData runs in the cbharness e2e.
//
// usage: lt_nfc_client host port frames
import Foundation

func hexs(_ b: [UInt8]) -> String { b.map { String(format: "%02x", $0) }.joined() }

let a = CommandLine.arguments
guard a.count >= 4 else { fputs("usage: lt_nfc_client host port frames\n", stderr); exit(2) }
let fd = socket(AF_INET, SOCK_STREAM, 0)
var sa = sockaddr_in()
sa.sin_family = sa_family_t(AF_INET)
sa.sin_port = in_port_t(UInt16(a[2])!).bigEndian
inet_pton(AF_INET, a[1], &sa.sin_addr)
let rc = withUnsafePointer(to: &sa) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
    connect(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) } }
guard rc == 0 else { perror("connect"); exit(2) }

func readN(_ n: Int) -> [UInt8]? {
    var b = [UInt8](repeating: 0, count: n); var got = 0
    while got < n { let r = b.withUnsafeMutableBytes { read(fd, $0.baseAddress! + got, n - got) }
        if r <= 0 { return nil }; got += r }
    return b
}
func writeAll(_ b: [UInt8]) { _ = b.withUnsafeBytes { write(fd, $0.baseAddress!, b.count) } }

struct NFCStatus: Error { let code: UInt8 }

/// One custom command 0xA1 over gen2sensor's NFC channel; the reply as CoreNFC
/// returns it.
func custom(_ parameters: Data) throws -> Data {
    writeAll([UInt8(parameters.count)] + [UInt8](parameters))
    guard let hdr = readN(2) else { throw NFCStatus(code: 0xff) }
    let reply = hdr[1] > 0 ? (readN(Int(hdr[1])) ?? []) : []
    if hdr[0] != 0 { print("NFC a1 \(hexs([UInt8](parameters.prefix(1)))): error 0x\(hexs([hdr[0]]))"); throw NFCStatus(code: hdr[0]) }
    return Data(reply)
}

guard let uidLine = readLine(), uidLine.count == 16 else { fputs("uid on stdin\n", stderr); exit(2) }
var uid = [UInt8](); var i = uidLine.startIndex
while i < uidLine.endIndex { let j = uidLine.index(i, offsetBy: 2); uid.append(UInt8(uidLine[i..<j], radix: 16)!); i = j }

writeAll([UInt8(ascii: "N")])
let result: Libre2Gen2PairingResult
do {
    result = try await Libre2Gen2Pairing.pair(uid: uid) { try custom($0) }
} catch {
    writeAll([0])
    print("NFC pairing FAILED: \(error)")
    exit(1)
}
writeAll([0])
print("NFC slot=\(result.rootSlot)")
print("NFC address=\(hexs([UInt8](result.bleAddress)))")
print("NFC provisioning=\(hexs([UInt8](result.streamingAuthData)))")
print("NFC fram=\(hexs([UInt8](result.fram)))")
print("NFC OK")

do {
    let session = try Libre2Gen2Session(uid: uid, keyIndex: result.rootSlot,
                                        streamingAuthData: [UInt8](result.streamingAuthData),
                                        keyStore: Libre2Gen2RootKeyStore())
    writeAll([Libre2Gen2.cmdReadChallenge])
    guard let challenge = readN(Libre2Gen2.challengeLength) else { print("no challenge"); exit(1) }
    writeAll(try session.sessionInfoCommand(challenge: challenge))
    guard let info = readN(Libre2Gen2.sessionInfoLength) else { print("no session info"); exit(1) }
    try session.open(sessionInfo: info)
    print("HANDSHAKE OK")
    let maxFrames = Int(a[3]) ?? 5
    var frames = 0, ok = 0
    while frames < maxFrames, let packet = readN(Libre2Gen2.packetLength) {
        frames += 1
        do { let p = try session.decrypt(packet: packet); ok += 1
            print("frame \(frames): CRC OK plain=\(hexs(p))") }
        catch { print("frame \(frames): \(error)") }
    }
    print("RESULT frames=\(frames) crcOK=\(ok)")
    exit(frames > 0 && ok == frames ? 0 : 1)
} catch {
    print("FAILED: \(error)"); exit(1)
}
