// Hosts SimulatorPairingNotice the way Libre2DirectSetup does, with the real
// SimulatedSensorPairingService, to see it in the Simulator.
import SwiftUI

@main
struct NoticeHarness: App {
    let service = SimulatedSensorPairingService()
    var body: some Scene {
        WindowGroup {
            NavigationView {
                ScrollView {
                    VStack(spacing: 16) {
                        SimulatorPairingNotice(pairingService: service).padding(.horizontal)
                        Button("Pair Sensor") { try? service.pairSensor() }.buttonStyle(.borderedProminent)
                    }
                }
                .navigationTitle("Libre 2 Setup")
            }
            // AUTOPAIR=1: tap Pair Sensor at launch, to see a failure without UI automation.
            .onAppear { if ProcessInfo.processInfo.environment["AUTOPAIR"] == "1" { try? service.pairSensor() } }
            .onReceive(service.publisher) { info in print("NOTICEHARNESS paired \(info.macAddress ?? "nil")") }
        }
    }
}
