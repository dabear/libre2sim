// algoharness: runs a LibreTransmitter checkout's own glucose algorithm on the records
// libre2sim made, and compares each minute with the glucose libre2sim intended.
// See README.md.

import Foundation

struct Readings: Decodable {
    struct Record: Decodable {
        let raw: Int
        let rawTemperature: Int
        let rawTemperatureAdjustment: Int
        let celsius: Double
        let tissueCelsius: Double
    }
    let firstMinute: Int
    let mgdl: [Int]
    let records: [Record]
    let calibration: [String: Int]
    let activatedMs: Double
}

let env = ProcessInfo.processInfo.environment
guard let path = env["READINGS"], let data = FileManager.default.contents(atPath: path),
      let r = try? JSONDecoder().decode(Readings.self, from: data) else {
    print("RESULT: FAIL set READINGS to a file holding libre2sim's GET /readings?minutes=n")
    exit(1)
}
let quiet = env["QUIET"] == "1"
let c = r.calibration
let calibration = SensorData.CalibrationInfo(i1: c["i1"]!, i2: c["i2"]!, i3: Double(c["i3"]!), i4: Double(c["i4"]!),
                                             i5: Double(c["i5"]!), i6: Double(c["i6"]!), isValidForFooterWithReverseCRCs: 0)
let activated = Date(timeIntervalSince1970: r.activatedMs / 1000)

// The minutes with a reading, oldest first, as LibreTransmitter's Measurements.
let minutes = r.records.indices.filter { r.records[$0].raw != 0 }
let measurements = minutes.map { i -> Measurement in
    let rec = r.records[i], minute = r.firstMinute + i
    return Measurement(date: activated.addingTimeInterval(Double(minute) * 60), rawGlucose: rec.raw,
                       rawTemperature: rec.rawTemperature, rawTemperatureAdjustment: rec.rawTemperatureAdjustment, idValue: minute)
}

// Each variant: a name and the mg/dL it computes for each measurement (nil: none).
var variants: [(String, [Double?])] = []
#if EXPERIMENTAL
// BATCH=libre2: as the app gets them, each minute computed from its own packet only (the
// readings 0, 2, 4, 6, 7, 12 and 15 minutes old), which is all the temperature filter sees.
// Otherwise the filter runs over all the minutes at once (a FRAM-like, gapless best case).
func smoothed(_ on: Bool) -> [Double?] {
    guard env["BATCH"] == "libre2" else {
        return GlucoseAlgorithm.calibratedGlucoses(for: measurements, calibrationInfo: calibration, smoothTemperature: on, sensorStart: activated)
    }
    let byMinute = Dictionary(uniqueKeysWithValues: measurements.map { ($0.idValue, $0) })
    return measurements.map { newest in
        let packet = [15, 12, 7, 6, 4, 2, 0].compactMap { byMinute[newest.idValue - $0] }
        return GlucoseAlgorithm.calibratedGlucoses(for: packet, calibrationInfo: calibration, smoothTemperature: on, sensorStart: activated).last ?? nil
    }
}
variants.append(("experimental, temperature smoothing off", smoothed(false)))
variants.append(("experimental, temperature smoothing on", smoothed(true)))
#else
variants.append(("glucoseValueFromRaw", measurements.map { Optional($0.calibratedGlucose(calibrationInfo: calibration)) }))
#endif

func f(_ v: Double?, _ w: Int = 7) -> String {
    guard let v else { return String(repeating: " ", count: w - 3) + "nil" }
    let s = String(format: "%.1f", v)
    return String(repeating: " ", count: max(0, w - s.count)) + s
}

for (name, values) in variants {
    var errors: [Double] = [], missing = 0
    if !quiet { print("== \(name)\n minute  wanted   skin°C tissue°C    got  error") }
    for (k, i) in minutes.enumerated() {
        let want = Double(r.mgdl[i]), rec = r.records[i]
        let got = values[k]
        if let got { errors.append(got - want) } else { missing += 1 }
        if !quiet {
            print(String(format: "%7d", r.firstMinute + i), f(want), f(rec.celsius, 8), f(rec.tissueCelsius, 8), f(got), f(got.map { $0 - want }))
        }
    }
    let n = Double(max(errors.count, 1))
    let mean = errors.reduce(0, +) / n
    let mae = errors.map(abs).reduce(0, +) / n
    let maxAbs = errors.map(abs).max() ?? 0
    print(String(format: "SUMMARY %@: %d minutes, %d without a value, mean error %+.2f, mean |error| %.2f, max |error| %.2f mg/dL",
                 name, errors.count, missing, mean, mae, maxAbs))
}
print("RESULT: OK")
