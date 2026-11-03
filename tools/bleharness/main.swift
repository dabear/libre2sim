// Independent end-to-end check of libre2sim's BLE side, built from LibreTransmitter's
// own Swift (PreLibre2, CRC, SensorData, GlucoseFromRaw, and Libre2NFC from the
// integration patch). It pairs over libre2sim's /nfc as the app does, then acts as
// an ImpossiBLE client on the provider socket: scan, match name == MAC, connect,
// discover FDE3/F001/F002, enable F002 notifications, write the real streaming
// unlock payload to F001, reassemble the 46-byte packet, and decode it with the
// real decryptBLE / parseBLEData / calibratedGlucose.
//
// Usage (env): LIBRE2SIM_URL (default http://127.0.0.1:8081),
//              IMPOSSIBLE_SOCKET (default /tmp/impossible-libre2.sock),
//              UNLOCK_COUNT (default 1), PACKETS (default 1)
import Foundation

let env = ProcessInfo.processInfo.environment
let base = URL(string: env["LIBRE2SIM_URL"] ?? "http://127.0.0.1:8081")!
let socketPath = env["IMPOSSIBLE_SOCKET"] ?? "/tmp/impossible-libre2.sock"
let unlockCount = UInt16(env["UNLOCK_COUNT"] ?? "1") ?? 1
let wantPackets = Int(env["PACKETS"] ?? "1") ?? 1

func fail(_ s: String) -> Never { print("RESULT: FAIL \(s)"); exit(1) }

func hexData(_ hex: String) -> Data {
    var d = Data(); var i = hex.startIndex
    while i < hex.endIndex { let j = hex.index(i, offsetBy: 2); d.append(UInt8(hex[i..<j], radix: 16)!); i = j }
    return d
}

func post(_ path: String, _ body: [String: Any]) -> [String: Any] {
    var req = URLRequest(url: base.appendingPathComponent(path))
    req.httpMethod = "POST"
    req.httpBody = try! JSONSerialization.data(withJSONObject: body)
    let sem = DispatchSemaphore(value: 0); var out: [String: Any] = [:]
    URLSession.shared.dataTask(with: req) { data, _, err in
        if let data, let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] { out = obj }
        else { out = ["error": "\(String(describing: err))"] }
        sem.signal()
    }.resume()
    sem.wait()
    return out
}

func get(_ path: String) -> [String: Any] {
    let sem = DispatchSemaphore(value: 0); var out: [String: Any] = [:]
    URLSession.shared.dataTask(with: base.appendingPathComponent(path)) { data, _, _ in
        if let data, let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] { out = obj }
        sem.signal()
    }.resume()
    sem.wait()
    return out
}

// --- NFC pairing, as SimulatedSensorPairingService does it ---
func nfc(_ body: [String: Any]) -> Data {
    let r = post("nfc", body)
    guard let h = r["response"] as? String else { fail("nfc \(body): \(r)") }
    return hexData(h)
}
let uid = Data(nfc(["op": "identifier"]).reversed())
_ = nfc(["op": "systemInfo"])
let info = nfc(["op": "custom", "code": 0xA1, "parameters": ""])
var fram = Data()
for first in stride(from: 0, to: 43, by: 3) {
    let r = post("nfc", ["op": "readBlocks", "first": first, "count": Swift.min(3, 43 - first)])
    for b in (r["blocks"] as? [String]) ?? [] { fram += hexData(b) }
}
let cmd = Libre2NFC.enableStreamingCommand(unlockCode: 42, patchInfo: info, sensorUID: uid)
let macResp = nfc(["op": "custom", "code": Int(cmd.code), "parameters": cmd.parameters.hexEncodedString()])
let mac = Data(macResp.reversed()).hexEncodedString().uppercased()
let plain = try Libre2.decryptFRAM(type: .libre2, id: [UInt8](uid), info: info, data: [UInt8](fram))
guard let sensorData = SensorData(bytes: plain) else { fail("FRAM header CRC") }
let cal = sensorData.calibrationData
print("paired uid \(uid.hexEncodedString()) mac \(mac) CRCs \(sensorData.hasValidCRCs)")

// --- ImpossiBLE client ---
let fd = socket(AF_UNIX, SOCK_STREAM, 0)
var addr = sockaddr_un(); addr.sun_family = sa_family_t(AF_UNIX)
_ = withUnsafeMutablePointer(to: &addr.sun_path) { p in
    socketPath.withCString { strncpy(UnsafeMutableRawPointer(p).assumingMemoryBound(to: CChar.self), $0, 103) }
}
let rc = withUnsafePointer(to: &addr) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) } }
guard rc == 0 else { fail("cannot connect to \(socketPath)") }
var tv = timeval(tv_sec: 90, tv_usec: 0)
setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, socklen_t(MemoryLayout<timeval>.size))

func send(_ msg: [String: Any]) {
    var d = try! JSONSerialization.data(withJSONObject: msg); d.append(0x0a)
    _ = d.withUnsafeBytes { write(fd, $0.baseAddress, d.count) }
}
var rbuf = Data()
func recv() -> [String: Any] {
    while true {
        if let nl = rbuf.firstIndex(of: 0x0a) {
            let line = rbuf[rbuf.startIndex..<nl]; rbuf = Data(rbuf[(nl + 1)...])
            if let o = try? JSONSerialization.jsonObject(with: line) as? [String: Any] { return o }
            continue
        }
        var chunk = [UInt8](repeating: 0, count: 4096)
        let n = read(fd, &chunk, chunk.count)
        guard n > 0 else { fail("socket closed or timed out") }
        rbuf.append(contentsOf: chunk[0..<n])
    }
}
func await_(_ type: String, where pred: ([String: Any]) -> Bool = { _ in true }) -> [String: Any] {
    while true { let m = recv(); if m["type"] as? String == type && pred(m) { return m } }
}

send(["type": "hello", "bundleId": "org.libre2sim.bleharness", "clientVersion": "harness", "pid": 0])
send(["type": "scan"])
let disc = await_("didDiscover") { $0["name"] as? String == mac }
let pid = disc["id"] as! String
let adv = disc["adv"] as? [String: Any] ?? [:]
print("discovered name \(disc["name"]!) services \(adv["kCBAdvDataServiceUUIDs"] ?? "-")")
send(["type": "stopScan"])
send(["type": "connect", "id": pid])
_ = await_("didConnect")
send(["type": "discoverServices", "id": pid])
let svcs = await_("didDiscoverServices")["services"] as! [[String: Any]]
guard let fde3 = svcs.first(where: { ($0["uuid"] as? String)?.uppercased().hasSuffix("FDE3") == true }) else { fail("no FDE3: \(svcs)") }
send(["type": "discoverCharacteristics", "id": pid, "serviceId": fde3["id"]!])
let chars = await_("didDiscoverCharacteristics")["characteristics"] as! [[String: Any]]
func charID(_ u: String) -> String {
    guard let c = chars.first(where: { ($0["uuid"] as? String)?.uppercased().hasSuffix(u) == true }) else { fail("no \(u)") }
    return c["id"] as! String
}
let f001 = charID("F001"), f002 = charID("F002")
send(["type": "setNotify", "id": pid, "characteristicId": f002, "enabled": true])
_ = await_("didUpdateNotification")
let unlock = Data(Libre2.streamingUnlockPayload(sensorUID: uid, info: info, enableTime: 42, unlockCount: unlockCount))
send(["type": "write", "id": pid, "characteristicId": f001, "value": unlock.base64EncodedString(), "writeType": 0])
let wr = await_("didWriteValue")
print("unlock count \(unlockCount) written, error '\(wr["error"] ?? "")'")

for n in 1...wantPackets {
    var packet = Data(); var sizes: [Int] = []
    while packet.count < 46 {
        let m = await_("didUpdateValue") { $0["characteristicId"] as? String == f002 }
        let v = Data(base64Encoded: m["value"] as! String)!
        sizes.append(v.count); packet += v
    }
    guard packet.count == 46 else { fail("buffer overshot to \(packet.count) bytes \(sizes)") }
    let dec = Data(try Libre2.decryptBLE(id: [UInt8](uid), data: [UInt8](packet)))
    let parsed = Libre2.parseBLEData(dec)
    let trend = parsed.trend.sorted { $0.idValue > $1.idValue }.map { "\($0.idValue):\(Int($0.calibratedGlucose(calibrationInfo: cal)))" }
    let hist = parsed.history.sorted { $0.idValue > $1.idValue }.map { "\($0.idValue):\(Int($0.calibratedGlucose(calibrationInfo: cal)))" }
    let st = get("status")
    print("packet \(n): chunks \(sizes) crc \(parsed.crcVerified) age \(parsed.age) | sim lifeCount \(st["lifeCount"] ?? "?") glucose \(st["glucose"] ?? "?")")
    print("  trend (minute:mg/dL) \(trend)")
    print("  history \(hist)")
}
print("RESULT: OK")
