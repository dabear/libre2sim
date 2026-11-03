// Stand-ins for the LoopKit-dependent pieces of LibreTransmitterProxyManager.swift,
// so the real Libre2DirectTransmitter plugin compiles on its own. Copied from
// LoopKit/LibreTransmitter @4110512 (MIT), trimmed; DeviceLogEntryType is LoopKit's.
import CoreBluetooth
import Foundation

public enum DeviceLogEntryType: String { case send, receive, error, delegate, delegateResponse, connection }

public enum BluetoothmanagerState: String {
    case Unassigned, Scanning, Disconnected, DelayedReconnect, DisconnectingDueToButtonPress
    case Connecting, Connected, Notifying, powerOff, UnknownDevice
}

public protocol LibreTransmitterDelegate: AnyObject {
    func libreDeviceStateChanged(_ state: BluetoothmanagerState)
    func libreDeviceReceivedMessage(_ txFlags: UInt8, payloadData: Data)
    func libreDeviceLogMessage(payload: String, type: DeviceLogEntryType)
    func libreTransmitterDidUpdate(with sensorData: SensorData, and Device: LibreTransmitterMetadata)
    func libreSensorDidUpdate(with bleData: Libre2.LibreBLEResponse, and Device: LibreTransmitterMetadata)
    func libreSensorDidUpdate(with error: LibreError)
}

// From LoopKit's UserDefaults helpers (used by LimitedQueue.swift).
extension UserDefaults {
    func optionalBool(forKey key: String) -> Bool? { object(forKey: key) as? Bool }
}
