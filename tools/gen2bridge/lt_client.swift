// SPDX-License-Identifier: GPL-3.0-or-later
//
// LibreTransmitter Gen2 client harness: drives LibreTransmitter's own
// Libre2Gen2Session (Libre2Gen2.swift + Libre2Gen2Credentials.swift, compiled
// in by run.sh from the LibreTransmitter branch) against gen2sensor over TCP.
// Output matches juggluco_client.c so run.sh compares both the same way.
//
// usage: lt_client host port uidHex provisioningHex rootSlot frames
import Foundation

func hex(_ s: String) -> [UInt8] {
    var out: [UInt8] = []
    var i = s.startIndex
    while i < s.endIndex, let j = s.index(i, offsetBy: 2, limitedBy: s.endIndex) {
        out.append(UInt8(s[i..<j], radix: 16)!); i = j
    }
    return out
}
func hexs(_ b: [UInt8]) -> String { b.map { String(format: "%02x", $0) }.joined() }

let a = CommandLine.arguments
guard a.count >= 7 else { fputs("usage: lt_client host port uid prov slot frames\n", stderr); exit(2) }
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

do {
    let slot = Int(a[5])!
    let store = Libre2Gen2RootKeyStore(defaultRootSlot: slot)
    let session = try Libre2Gen2Session(uid: hex(a[3]), keyIndex: slot,
                                        streamingAuthData: hex(a[4]), keyStore: store)
    writeAll([Libre2Gen2.cmdReadChallenge])
    guard let challenge = readN(Libre2Gen2.challengeLength) else { print("no challenge"); exit(1) }
    writeAll(try session.sessionInfoCommand(challenge: challenge))
    guard let info = readN(Libre2Gen2.sessionInfoLength) else { print("no session info"); exit(1) }
    try session.open(sessionInfo: info)
    print("HANDSHAKE OK")
    let maxFrames = Int(a[6]) ?? 5
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
