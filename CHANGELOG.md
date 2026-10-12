# Changelog

## [0.2.0](https://github.com/lscythe/jalvio-service/compare/v0.1.0...v0.2.0) (2026-10-12)


### Features

* **mqttfeed:** count skipped messages by reason ([#19](https://github.com/lscythe/jalvio-service/issues/19)) ([cefc5ea](https://github.com/lscythe/jalvio-service/commit/cefc5ea96a1a4ce9b87f2fecc9948f95c4410f4e))

## 0.1.0 (2026-10-11)


### Features

* add service skeleton with health and status endpoints ([337b959](https://github.com/lscythe/jalvio-service/commit/337b959dd594d443ed414f63f7152877cfd10c11))
* **atomicfile:** add crash-safe file replacement ([5b1fcd7](https://github.com/lscythe/jalvio-service/commit/5b1fcd79f6ec26105680a7c2a7e1a97ccf113e55))
* **catalog:** add in-memory stop and route search ([afb4760](https://github.com/lscythe/jalvio-service/commit/afb47602fd224fd4998b4f3b193d9bac8470f7d6))
* **catalog:** parse the GTFS feed into an immutable catalog ([030d72c](https://github.com/lscythe/jalvio-service/commit/030d72c4a3f0fe329537428f3b9377732ecd1222))
* **catalog:** refresh the feed and keep the last good copy on disk ([b88ac96](https://github.com/lscythe/jalvio-service/commit/b88ac962ed12238046bbe94502aa71372709e538))
* **fleet:** track live bus state from the position feed ([#6](https://github.com/lscythe/jalvio-service/issues/6)) ([fcc25a9](https://github.com/lscythe/jalvio-service/commit/fcc25a9957aec94450804e29b1eed81dbbbe672d))
* load and refresh the GTFS catalog at startup ([e1c6037](https://github.com/lscythe/jalvio-service/commit/e1c6037a48f31601df4b4a03c73bd7890f689336))
* **places:** add destination search through a Photon geocoder ([#12](https://github.com/lscythe/jalvio-service/issues/12)) ([4e5ec3d](https://github.com/lscythe/jalvio-service/commit/4e5ec3dcbae7fa99c63751cdbf0eab6ea7bd3807))
* run the stop facilities sync and report it in status ([#11](https://github.com/lscythe/jalvio-service/issues/11)) ([19919d8](https://github.com/lscythe/jalvio-service/commit/19919d87cdad0aa1afcb2320ceac80e88900a232))
* ship a distroless container image with compose ([#13](https://github.com/lscythe/jalvio-service/issues/13)) ([aa948dc](https://github.com/lscythe/jalvio-service/commit/aa948dc3508fa50b95c39a19d9a1f51cd1a93490))
* **stopapi:** add a guest-session client for the stop API ([#9](https://github.com/lscythe/jalvio-service/issues/9)) ([ba3920f](https://github.com/lscythe/jalvio-service/commit/ba3920f85058263b786fa642be7ec35bcd140922))
* **stopinfo:** sync halte facilities from the stop API ([#10](https://github.com/lscythe/jalvio-service/issues/10)) ([7cf389d](https://github.com/lscythe/jalvio-service/commit/7cf389d7ea7c016715b23ada0a0a888fd4e11d46))
* stream live bus positions from the MQTT broker ([#7](https://github.com/lscythe/jalvio-service/issues/7)) ([7f227fb](https://github.com/lscythe/jalvio-service/commit/7f227fba3bfe2bde1d31e9bb54e0decff4ec273a))


### Bug Fixes

* **release:** start versioning at 0.1.0 ([#17](https://github.com/lscythe/jalvio-service/issues/17)) ([077e11c](https://github.com/lscythe/jalvio-service/commit/077e11c44218d2a4ba01cdf2eeeb02f7a8fc273f))
