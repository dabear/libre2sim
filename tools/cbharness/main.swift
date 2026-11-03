// End-to-end check through the real CoreBluetooth API: run in the iOS Simulator with
// libImpossiBLE.dylib injected, so CBCentralManager talks to libre2sim. It pairs over
// /nfc with LibreTransmitter's SimulatedSensorPairingService, stores the Sensor as the
// setup UI does, then plays LibreTransmitterProxyManager's part (scan for the plugins'
// services, match name == MAC, connect, discover, hand characteristics and values to
// the plugin) for LibreTransmitter's real Libre2DirectTransmitter.
import Combine
import CoreBluetooth
import Foundation

let wantPackets = Int(ProcessInfo.processInfo.environment["PACKETS"] ?? "2") ?? 2
// FOLLOW=1: on a drop, reconnect to the known peripheral as LibreTransmitterProxyManager does
// (Libre2DirectTransmitter.requiresDelayedReconnect is false, so at once); the connect is held
// until the sensor advertises again. TIMEOUT=s: give up after s seconds (default 150).
let follow = ProcessInfo.processInfo.environment["FOLLOW"] == "1"
let timeout = Double(ProcessInfo.processInfo.environment["TIMEOUT"] ?? "150") ?? 150
let tolerateErrors = ProcessInfo.processInfo.environment["TOLERATE_ERRORS"] == "1"
var calibration: SensorData.CalibrationInfo!
var bag = Set<AnyCancellable>()

final class Central: NSObject, CBCentralManagerDelegate, CBPeripheralDelegate, LibreTransmitterDelegate {
    var manager: CBCentralManager!
    var peripheral: CBPeripheral?
    var plugin: Libre2DirectTransmitter?
    var packets = 0
    let mac: String

    init(mac: String) {
        self.mac = mac
        super.init()
        manager = CBCentralManager(delegate: self, queue: .main)
    }

    func centralManagerDidUpdateState(_ central: CBCentralManager) {
        print("central state \(central.state.rawValue)")
        guard central.state == .poweredOn else { return }
        let services = Libre2DirectTransmitter.serviceUUID.map { $0.value }
        print("scanning for \(services)")
        central.scanForPeripherals(withServices: services, options: nil)
    }

    func centralManager(_ central: CBCentralManager, didDiscover peripheral: CBPeripheral, advertisementData: [String: Any], rssi RSSI: NSNumber) {
        print("didDiscover name=\(peripheral.name ?? "nil") canSupport=\(Libre2DirectTransmitter.canSupportPeripheral(peripheral))")
        guard peripheral.name == mac, Libre2DirectTransmitter.canSupportPeripheral(peripheral) else { return }
        central.stopScan()
        self.peripheral = peripheral
        plugin = Libre2DirectTransmitter(delegate: self, advertisementData: advertisementData)
        peripheral.delegate = self
        central.connect(peripheral, options: nil)
    }

    func centralManager(_ central: CBCentralManager, didConnect peripheral: CBPeripheral) {
        print("didConnect")
        peripheral.discoverServices(Libre2DirectTransmitter.serviceUUID.map { $0.value })
    }

    func centralManager(_ central: CBCentralManager, didDisconnectPeripheral peripheral: CBPeripheral, error: Error?) {
        print("didDisconnect \(String(describing: error)) after \(packets) packets")
        guard follow else { return }
        // as LibreTransmitterProxyManager.didDisconnectPeripheral → reconnect() → connect(advertisementData: nil)
        plugin?.reset()
        print("reconnecting")
        central.connect(peripheral, options: nil)
    }

    func peripheral(_ peripheral: CBPeripheral, didDiscoverServices error: Error?) {
        for s in peripheral.services ?? [] {
            print("service \(s.uuid)")
            let chars = [Libre2DirectTransmitter.writeCharacteristic, Libre2DirectTransmitter.notifyCharacteristic].compactMap { $0?.value }
            peripheral.discoverCharacteristics(chars, for: s)
        }
    }

    // As LibreTransmitterProxyManager.peripheral(_:didDiscoverCharacteristicsFor:error:)
    func peripheral(_ peripheral: CBPeripheral, didDiscoverCharacteristicsFor service: CBService, error: Error?) {
        for c in service.characteristics ?? [] {
            print("characteristic \(c.uuid) props \(c.properties.rawValue)")
            if c.properties.intersection(.notify) == .notify && c.uuid == Libre2DirectTransmitter.notifyCharacteristic?.value {
                plugin?.didDiscoverNotificationCharacteristic(peripheral, notifyCharacteristic: c)
            }
            if c.uuid == Libre2DirectTransmitter.writeCharacteristic?.value {
                plugin?.didDiscoverWriteCharacteristics(peripheral, writeCharacteristics: c)
            }
        }
    }

    func peripheral(_ peripheral: CBPeripheral, didUpdateNotificationStateFor characteristic: CBCharacteristic, error: Error?) {
        print("notify state \(characteristic.uuid) \(characteristic.isNotifying) error \(String(describing: error))")
        plugin?.reset()
    }

    func peripheral(_ peripheral: CBPeripheral, didWriteValueFor characteristic: CBCharacteristic, error: Error?) {
        print("didWrite \(characteristic.uuid) error \(String(describing: error))")
    }

    func peripheral(_ peripheral: CBPeripheral, didUpdateValueFor characteristic: CBCharacteristic, error: Error?) {
        guard let v = characteristic.value else { return }
        plugin?.updateValueForNotifyCharacteristics(v, peripheral: peripheral, writeCharacteristic: nil)
    }

    // LibreTransmitterDelegate
    func libreDeviceStateChanged(_ state: BluetoothmanagerState) {}
    func libreDeviceReceivedMessage(_ txFlags: UInt8, payloadData: Data) {}
    func libreDeviceLogMessage(payload: String, type: DeviceLogEntryType) { print("  [plugin \(type)] \(payload.prefix(120))") }
    func libreTransmitterDidUpdate(with sensorData: SensorData, and Device: LibreTransmitterMetadata) {}
    func libreSensorDidUpdate(with error: LibreError) {
        if tolerateErrors {
            print("PLUGIN ERROR \(error) (after \(packets) packets)")
            return
        }
        print("RESULT: FAIL plugin error \(error)"); exit(1)
    }
    func libreSensorDidUpdate(with bleData: Libre2.LibreBLEResponse, and Device: LibreTransmitterMetadata) {
        packets += 1
        let trend = bleData.trend.sorted { $0.idValue > $1.idValue }.map { "\($0.idValue):\(Int($0.calibratedGlucose(calibrationInfo: calibration)))" }
        let hist = bleData.history.sorted { $0.idValue > $1.idValue }.map { "\($0.idValue):\(Int($0.calibratedGlucose(calibrationInfo: calibration)))" }
        print("PACKET \(packets): age \(bleData.age) crc \(bleData.crcVerified) unlockCount \(UserDefaults.standard.preSelectedSensor?.unlockCount ?? -1)")
        print("  trend \(trend)")
        print("  history \(hist)")
        if packets >= wantPackets { print("RESULT: OK"); exit(0) }
    }
}

var central: Central?
// RECONNECT=1: skip NFC and reuse the stored Sensor, as LibreTransmitter does when it
// reconnects to a known sensor (the plugin bumps unlockCount for every connection).
if ProcessInfo.processInfo.environment["RECONNECT"] == "1" {
    guard let sensor = UserDefaults.standard.preSelectedSensor, let mac = sensor.macAddress,
          let fram = UserDefaults.standard.data(forKey: "cbharness.fram"), let sd = SensorData(bytes: [UInt8](fram)) else {
        print("RESULT: FAIL no stored sensor; run once without RECONNECT"); exit(1)
    }
    calibration = sd.calibrationData
    // REPLAY=n rewinds the stored count so the plugin sends count n again (strict mode must refuse it).
    if let n = Int(ProcessInfo.processInfo.environment["REPLAY"] ?? "") {
        var s = sensor; s.unlockCount = n - 1; UserDefaults.standard.preSelectedSensor = s
    }
    print("reconnecting: mac \(mac) stored unlockCount \(UserDefaults.standard.preSelectedSensor?.unlockCount ?? -1)")
    central = Central(mac: mac)
    DispatchQueue.main.asyncAfter(deadline: .now() + timeout) { print("RESULT: FAIL timeout"); exit(1) }
    RunLoop.main.run()
}
let pairing = SimulatedSensorPairingService()
pairing.onCancel = { print("RESULT: FAIL pairing"); exit(1) }
pairing.publisher.sink { info in
    guard let sd = info.sensorData, let mac = info.macAddress else { print("RESULT: FAIL no sensor data / mac"); exit(1) }
    calibration = sd.calibrationData
    // as Libre2DirectSetup does after a scan
    UserDefaults.standard.preSelectedSensor = Sensor(uuid: info.uuid, patchInfo: info.patchInfo, maxAge: sd.maxMinutesWearTime, sensorName: info.sensorName, macAddress: mac)
    UserDefaults.standard.set(info.fram, forKey: "cbharness.fram")
    print("paired: mac \(mac) state \(sd.state.description) age \(sd.minutesSinceStart)")
    central = Central(mac: mac)
}.store(in: &bag)
try pairing.pairSensor()
DispatchQueue.main.asyncAfter(deadline: .now() + timeout) { print("RESULT: FAIL timeout (packets \(central?.packets ?? 0))"); exit(1) }
RunLoop.main.run()
