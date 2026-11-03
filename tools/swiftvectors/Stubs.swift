// Minimal stand-ins for LibreTransmitter types that the copied files refer to
// but that do not affect the crypto. Only what is needed to compile.

import Foundation

// Bluetooth/LibreTransmitterMetadata.swift (cases only; decryptFRAM switches on it).
public enum SensorType: String {
    case libre1       = "Libre 1"
    case libreUS14day = "Libre US 14d"
    case libreProH    = "Libre Pro/H"
    case libre2       = "Libre 2"
    case libre2US     = "Libre 2 US"
    case libre2CA     = "Libre 2 CA"
    case libreSense   = "Libre Sense"
    case libre3       = "Libre 3"
    case dexcomOne    = "Dexcom ONE"
    case dexcomG7     = "Dexcom G7"
    case unknown      = "Libre"
}

// LibreSensor/SensorContents/Measurement.swift: parseBLEData builds these.
public struct Measurement {
    let date: Date
    let rawGlucose: Int
    let rawTemperature: Int
    let rawTemperatureAdjustment: Int
    let idValue: Int
}

// LibreSensor/SensorPairing/SensorPairing.swift: FakeSensorPairingData returns one.
public struct SensorPairingInfo {
    init(uuid: Data, patchInfo: Data, fram: Data, streamingEnabled: Bool, sensorName: String) {}
}
