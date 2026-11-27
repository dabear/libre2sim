// SPDX-License-Identifier: GPL-3.0-or-later
//
// LibreTransmitter side of gen2diff: answers the commands juggluco_peer.c
// answers (see its header), through LibreTransmitter's own Libre2Gen2.swift +
// Libre2Gen2Credentials.swift, compiled in by run.sh from the branch under test.
import Foundation

func hex(_ s: Substring) -> [UInt8]? {
    guard s.count % 2 == 0 else { return nil }
    var out: [UInt8] = []
    var i = s.startIndex
    while i < s.endIndex {
        let j = s.index(i, offsetBy: 2)
        guard let b = UInt8(s[i..<j], radix: 16) else { return nil }
        out.append(b); i = j
    }
    return out
}
func hexs(_ b: [UInt8]) -> String { b.map { String(format: "%02x", $0) }.joined() }

setvbuf(stdout, nil, _IOLBF, 0)

let store = Libre2Gen2RootKeyStore()
var ble: Libre2Gen2Session?
var nfc: Libre2Gen2NFCExchange?

func credential(_ slot: Substring) throws -> Libre2Gen2Credential {
    guard let s = Int(slot), let c = store.credential(forIndex: s) else {
        throw Libre2Gen2.Libre2Gen2Error("no root slot \(slot)")
    }
    return c
}

func answer(_ f: [Substring]) throws -> [UInt8] {
    func arg(_ i: Int) throws -> [UInt8] {
        guard let b = hex(f[i]) else { throw Libre2Gen2.Libre2Gen2Error("args") }
        return b
    }
    switch (f.first ?? "", f.count - 1) {
    case ("KEY", 2):
        return try credential(f[1]).kauth(uid: try arg(2))
    case ("BLEREQ", 5):
        ble = nil
        let session = try Libre2Gen2Session(uid: try arg(2), credential: try credential(f[1]),
                                            streamingAuthData: try arg(3))
        ble = session
        return try session.sessionInfoCommand(challenge: try arg(4), random: try arg(5))
    case ("BLEOPEN", 1):
        guard let ble else { throw Libre2Gen2.Libre2Gen2Error("no session") }
        return try ble.open(sessionInfo: try arg(1))
    case ("BLEDEC", 1):
        guard let ble else { throw Libre2Gen2.Libre2Gen2Error("no session") }
        return try ble.decrypt(packet: try arg(1), verify: false)
    case ("BLEDECK", 2):
        guard let ble else { throw Libre2Gen2.Libre2Gen2Error("no session") }
        let saved = ble.sessionKey
        defer { if let saved { try? ble.resume(sessionKey: saved) } else { ble.reset() } }
        try ble.resume(sessionKey: try arg(1))
        return try ble.decrypt(packet: try arg(2), verify: false)
    case ("NFCREQ", 5):
        nfc = nil
        let exchange = try Libre2Gen2NFCExchange(uid: try arg(2), credential: try credential(f[1]))
        let command = try arg(3)
        guard command.count == 1 else { throw Libre2Gen2.Libre2Gen2Error("args") }
        let request = try exchange.request(challenge: try arg(4), command: command[0], random: try arg(5))
        nfc = exchange
        return request
    case ("NFCDEC", 4):
        guard let nfc else { throw Libre2Gen2.Libre2Gen2Error("no exchange") }
        guard let first = Int(f[2]), let count = Int(f[3]) else { throw Libre2Gen2.Libre2Gen2Error("args") }
        let cipher = try arg(4)
        guard cipher.count == 8 * count else { throw Libre2Gen2.Libre2Gen2Error("args") }
        return try nfc.decryptBlocks(dataKey: try arg(1), firstBlock: first, encrypted: cipher)
    case ("CRC", 1):
        let c = Libre2Gen2.crc16(try arg(1))
        return [UInt8(c & 0xFF), UInt8(c >> 8)]
    default:
        throw Libre2Gen2.Libre2Gen2Error("unknown")
    }
}

while let line = readLine() {
    let f = line.split(separator: " ")
    do { print("OK " + hexs(try answer(f))) } catch { print("ERR \(error)") }
}
