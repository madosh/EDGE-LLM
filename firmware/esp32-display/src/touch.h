// Tap detection on the FT6336G touch controller. The UI only needs "the
// screen was tapped", so coordinates are not read and no calibration is
// needed. Boards without the touch panel (ES3N28P) simply never report a tap.
#pragma once

class TapSensor {
 public:
  // Resets the controller and checks it answers. Returns false if absent.
  bool begin();
  // True once per tap, when the finger lifts.
  bool tapped();

 private:
  bool present_ = false;
  bool down_ = false;
};
