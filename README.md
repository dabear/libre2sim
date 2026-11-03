# libre2sim

A FreeStyle Libre 2 Plus (EU) sensor emulator, for exercising
[LibreTransmitter](https://github.com/LoopKit/LibreTransmitter) without a real sensor. It serves an
app in the iOS Simulator through [ImpossiBLE](https://github.com/mickeyl/ImpossiBLE), the way
[libre3sim](https://github.com/LoopKit/libre3sim) does for the Libre 3.

> **For development use only — not an Abbott product.** libre2sim is an independent, unofficial tool
> for testing CGM software. It is not made, endorsed, or supported by Abbott, it is not a medical
> device, and it emits only simulated, made-up data. "FreeStyle Libre" and "Libre" are trademarks of
> Abbott, used here solely to describe interoperability. Never use it for therapy or any clinical
> decision.

Status: under construction — see [libre2-implementation-plan.md](libre2-implementation-plan.md).

## Credits

- Libre 2 protocol and crypto as implemented in LibreTransmitter (and the LibreDirect / xDrip work it
  builds on).
- iOS Simulator transport, state restoration and suspension from libre3sim / g7sim (MIT).
