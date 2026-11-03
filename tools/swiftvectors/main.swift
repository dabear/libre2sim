// Prints golden test vectors for libre2sim's Go port of the Libre 2 crypto,
// computed by LibreTransmitter's own Swift code (PreLibre2.swift, CRC.swift and
// the nfcCommand excerpt in Pairing.swift, all copied verbatim).
// See README.md for how to regenerate testdata/swift_vectors.json.

import Foundation

func hex<S: Sequence>(_ b: S) -> String where S.Element == UInt8 {
    b.map { String(format: "%02x", $0) }.joined()
}

func words(_ w: [UInt16]) -> [Int] { w.map { Int($0) } }

let bleUID = Libre2.BLEExample.sensorId
let ex2Info = Libre2.Example2.sensorInfo
let fake = FakeSensorPairingData()

// (name, uid, info, encrypted FRAM)
let frams: [(String, [UInt8], [UInt8], [UInt8])] = [
    ("Example", Libre2.Example.sensorId, Libre2.Example.sensorInfo, Libre2.Example.buffer),
    ("Example2", Libre2.Example2.sensorId, Libre2.Example2.sensorInfo, Libre2.Example2.buffer),
    ("FakeSensorPairingData", fake.de_new_patch_uid, fake.de_new_patch_info, fake.de_new_packet),
]

let oddUID: [UInt8] = [0xff, 0xfe, 0xfd, 0xfc, 0xfb, 0xfa, 0x07, 0xe0]
let uids: [[UInt8]] = [bleUID, Libre2.Example.sensorId, fake.de_new_patch_uid, oddUID]

var out: [String: Any] = [:]
out["source"] = "LoopKit/LibreTransmitter@41105123c3c4fb5c55ce89a38630cea2f8f7a17a"

var pc: [[String: Any]] = []
for input: [UInt16] in [[0, 0, 0, 0], [0xffff, 0xffff, 0xffff, 0xffff], [0x1234, 0x5678, 0x9abc, 0xdef0], [0xA0C5, 0x6860, 0x0000, 0x14C6]] {
    pc.append(["in": words(input), "out": words(Libre2.processCrypto(input: input))])
}
out["processCrypto"] = pc

let xys: [(UInt16, UInt16)] = [(0x1b, 0x1b6a), (0x1e, 0x1234), (0, 0), (0xffff, 0xffff), (42, 0x8001)]
var pv: [[String: Any]] = []
var uf: [[String: Any]] = []
for uid in uids {
    for (x, y) in xys {
        pv.append(["uid": hex(uid), "x": Int(x), "y": Int(y), "out": words(Libre2.prepareVariables(id: uid, x: x, y: y))])
        let a = Libre2.usefulFunction(id: uid, x: x, y: y)
        let b = Libre2.usefulFunction(sensorUID: Data(uid), x: x, y: y)
        precondition(a == b, "usefulFunction variants differ")
        uf.append(["uid": hex(uid), "x": Int(x), "y": Int(y), "out": hex(a)])
    }
}
out["prepareVariables"] = pv
out["usefulFunction"] = uf

var pv4: [[String: Any]] = []
for uid in uids {
    for i: [UInt16] in [[0, 0, 0, 0], [0xffff, 0xffff, 0xffff, 0xffff], [1, 2, 3, 4], [0x8000, 0x7fff, 0xedcb, 0x1357]] {
        pv4.append(["uid": hex(uid), "i": words(i),
                    "out": words(Libre2.prepareVariables(sensorUID: Data(uid), i1: i[0], i2: i[1], i3: i[2], i4: i[3]))])
    }
}
out["prepareVariables4"] = pv4

// (uid, info) pairs used for the enable-streaming command and the unlock payload.
let pairs: [([UInt8], [UInt8])] = [
    (bleUID, ex2Info),
    (Libre2.Example.sensorId, Libre2.Example.sensorInfo),
    (fake.de_new_patch_uid, fake.de_new_patch_info),
]

var es: [[String: Any]] = []
for (uid, info) in pairs {
    for code: UInt32 in [42, 0xdeadbeef] {
        let cmd = Pairing.nfcCommand(.enableStreaming, unlockCode: code, patchInfo: Data(info), sensorUID: Data(uid))
        es.append(["uid": hex(uid), "info": hex(info), "code": Int(code), "cmdCode": Int(cmd.code), "params": hex(cmd.parameters)])
    }
}
out["enableStreamingParams"] = es

var ul: [[String: Any]] = []
for (uid, info) in pairs {
    for (t, n): (UInt32, UInt16) in [(42, 1), (42, 2), (42, 3), (42, 65535), (0x12345678, 7)] {
        let p = Libre2.streamingUnlockPayload(sensorUID: Data(uid), info: Data(info), enableTime: t, unlockCount: n)
        ul.append(["uid": hex(uid), "info": hex(info), "enableTime": Int(t), "count": Int(n), "payload": hex(p)])
    }
}
out["unlockPayload"] = ul

var crcs: [[String: Any]] = []
let crcInputs: [[UInt8]] = [
    [],
    [0x00],
    Array("123456789".utf8),
    Array(0 ... 255),
    [0xc1, 0xc4, 0xc3, 0xc0, 0xd4, 0xe1, 0xe7, 0xba, 0x12, 0x34],
]
for b in crcInputs {
    crcs.append(["data": hex(b), "crc": Int(Crc.crc16(b, seed: 0xffff))])
}
out["crc16"] = crcs

var fr: [[String: Any]] = []
for (name, uid, info, enc) in frams {
    let plain = try Libre2.decryptFRAM(type: .libre2, id: uid, info: Data(info), data: enc)
    fr.append(["name": name, "uid": hex(uid), "info": hex(info), "encrypted": hex(enc),
               "plainFirst16": hex(plain[0 ..< 16]), "plain": hex(plain),
               "headerCRCValid": Crc.hasValidCrc16InFirstTwoBytes(Array(plain[0 ..< 24])),
               "bodyCRCValid": Crc.hasValidCrc16InFirstTwoBytes(Array(plain[24 ..< 320])),
               "footerCRCValid": Crc.hasValidCrc16InFirstTwoBytes(Array(plain[320 ..< 344]))])
}
out["decryptFRAM"] = fr

let blePlain = try Libre2.decryptBLE(id: bleUID, data: Libre2.BLEExample.data)
let parsed = Libre2.parseBLEData(Data(blePlain))
out["decryptBLE"] = [["uid": hex(bleUID), "data": hex(Libre2.BLEExample.data), "plain": hex(blePlain),
                      "age": parsed.age, "crcVerified": parsed.crcVerified,
                      "trendRaw": parsed.trend.map { $0.rawGlucose }, "historyRaw": parsed.history.map { $0.rawGlucose }]]

let json = try JSONSerialization.data(withJSONObject: out, options: [.prettyPrinted, .sortedKeys])
FileHandle.standardOutput.write(json)
FileHandle.standardOutput.write("\n".data(using: .utf8)!)
