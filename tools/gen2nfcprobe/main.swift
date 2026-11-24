// Replays LibreTransmitter's Gen2 NFC pairing (SensorPairingService+Gen2.swift,
// pairGen2) against libre2sim's /nfc, step by step and in its order, with LT's
// own request bytes and parsers; only CoreNFC's customCommand is replaced by an
// HTTP POST, as SimulatedSensorPairingService does for gen1. It reports where a
// sensor that follows Juggluco's Gen2 protocol (libre2sim's Gen2 mode) stops it.
// Build and run as tools/cbharness, with this main.swift instead of cbharness's.
import Foundation

let base = URL(string: ProcessInfo.processInfo.environment["LIBRE2SIM_URL"] ?? "http://127.0.0.1:8081")!

func nfc(_ body: [String: Any]) -> (Data?, String?) {
    var req = URLRequest(url: base.appendingPathComponent("nfc"))
    req.httpMethod = "POST"; req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.httpBody = try! JSONSerialization.data(withJSONObject: body)
    let sem = DispatchSemaphore(value: 0); var out: (Data?, String?) = (nil, "no reply")
    URLSession.shared.dataTask(with: req) { data, resp, err in
        defer { sem.signal() }
        guard let data, let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { out = (nil, err?.localizedDescription); return }
        if let hex = obj["response"] as? String {
            var b = [UInt8](); var i = hex.startIndex
            while i < hex.endIndex { let j = hex.index(i, offsetBy: 2); b.append(UInt8(hex[i..<j], radix: 16)!); i = j }
            out = (Data(b), nil)
        } else { out = (nil, "\(obj["error"] ?? "?") (NFC code \(obj["code"] ?? "?"))") }
    }.resume()
    sem.wait(); return out
}
func custom(_ p: Data) -> (Data?, String?) { nfc(["op": "custom", "code": 0xA1, "parameters": p.hexEncodedString()]) }

let uid = Data(nfc(["op": "identifier"]).0!.reversed())
let patchInfo = custom(Data()).0!
let gen = (try? Libre2Gen2.securityGeneration(patchInfo: [UInt8](patchInfo))) ?? -1
print("patchInfo \(patchInfo.hexEncodedString()) -> LT securityGeneration \(gen)")
guard gen == 2 else { print("PROBE: sensor is not Gen2 for LT"); exit(1) }
var failures = 0

// 1. readGen2FRAM: A1 21 <block> <count-1>, unauthenticated, expects 24 or 32 bytes of plain FRAM.
let r1 = custom(Data([0x21, 0, 2]))
if let d = r1.0 { print("step 1 readGen2FRAM A1 21 00 02 -> \(d.count) bytes \(d.hexEncodedString())") }
else { print("step 1 readGen2FRAM A1 21 00 02 -> REFUSED: \(r1.1!)  => LT: completion(nil) -> fail(.noSensorData)"); failures += 1 }

// 2. readKeyIndex: A1 22, parsed with LT's parseKeyIndex at gen2KeyIndexOffset.
let r2 = custom(Data([0x22]))
if let d = r2.0 {
    let idx = (try? Libre2Gen2.parseKeyIndex(response: [UInt8](d), offset: SensorPairingService.gen2KeyIndexOffset)) ?? -1
    let juggluco = d.count >= 4 ? Int(d[2]) | Int(d[3]) << 8 : -1
    let known = Libre2Gen2RuntimeKeyStore.shared.credential(forIndex: idx) != nil
    print("step 2 readKeyIndex A1 22 -> \(d.hexEncodedString()); LT parseKeyIndex(offset \(SensorPairingService.gen2KeyIndexOffset)) = \(idx) (credential \(known ? "found" : "NOT FOUND")); Juggluco raw[3..4] = \(juggluco)")
    if !known || idx != juggluco { failures += 1 }
} else { print("step 2 readKeyIndex -> REFUSED: \(r2.1!)"); failures += 1 }

// 3. enable streaming: LT's enableStreamingRequest (the gen1 0x1E), expects a 10- or 12-byte reply.
let req = SensorPairingService().enableStreamingRequest(patchInfo: patchInfo, sensorUID: uid)
let r3 = nfc(["op": "custom", "code": req.code, "parameters": req.parameters.hexEncodedString()])
if let d = r3.0 { print("step 3 enableStreamingRequest \(String(format: "%02X", req.code)) \(req.parameters.hexEncodedString()) -> \(d.count) bytes \(d.hexEncodedString())") }
else { print("step 3 enableStreamingRequest \(String(format: "%02X", req.code)) \(req.parameters.hexEncodedString()) -> REFUSED: \(r3.1!)"); failures += 1 }

print(failures == 0 ? "PROBE: LT's Gen2 NFC pairing would pass" : "PROBE: LT's Gen2 NFC pairing FAILS at \(failures) of 3 steps")
exit(failures == 0 ? 0 : 1)
