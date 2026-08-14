<?php
// Long-lived service that writes one line per second, starting with "Hello 0".
for ($x = 0; $x <= 1000; $x++) {
  error_log("Hello $x", 0);
  sleep(1);
}

exit();
