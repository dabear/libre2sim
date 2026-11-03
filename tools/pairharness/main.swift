// Independent check: LibreTransmitter's own Simulator pairing service (M3 patch)
// pairing against a running libre2sim, inside the iOS Simulator.
import Foundation
import Combine

let service = SimulatedSensorPairingService()
var bag = Set<AnyCancellable>()
var done = false
service.onCancel = { print("RESULT: cancelled (pairing failed)"); done = true }
service.publisher.sink { info in
    print("RESULT: paired")
    print("uuid", info.uuid.hexEncodedString(), "patchInfo", info.patchInfo.hexEncodedString())
    print("streamingEnabled", info.streamingEnabled, "mac", info.macAddress ?? "nil")
    if let sd = info.sensorData {
        print("fram CRCs valid", sd.hasValidCRCs, "state", sd.state.description)
        print("minutesSinceStart", sd.minutesSinceStart, "maxMinutesWearTime", sd.maxMinutesWearTime)
        if var sd2 = SensorData(uuid: info.uuid, bytes: [UInt8](info.fram)) { sd2.patchInfo = info.patchInfo; print("serial", sd2.serialNumber) }
        print("calibration", sd.calibrationData.description)
        print("SensorType", SensorType(patchInfo: info.patchInfo).description)
    } else { print("sensorData nil") }
    done = true
}.store(in: &bag)
try service.pairSensor()
let deadline = Date().addingTimeInterval(20)
while !done && Date() < deadline { RunLoop.main.run(until: Date().addingTimeInterval(0.1)) }
if !done { print("RESULT: timeout") }
