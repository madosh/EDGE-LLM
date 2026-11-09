// Tap detection on the FT6336G touch controller. A tap is reported when the
// finger lifts, with the last point it touched, in panel pixels (origin at
// the top left, matching the display's rotation). Boards without the touch
// panel (ES3N28P) simply never report a tap; the BOOT button covers that.
#pragma once

class TapSensor {
 public:
  // Resets the controller and checks it answers. Returns false if absent.
  bool begin();
  // True once per tap. x and y are the lift point.
  bool take(int& x, int& y);

 private:
  bool present_ = false;
  bool down_ = false;
  bool havePoint_ = false;
  int lastX_ = 0;
  int lastY_ = 0;
};
