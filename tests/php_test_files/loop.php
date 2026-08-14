<?php
// Long-lived service that writes one line per second, starting with "The number is: 0".
for ($x = 0; $x <= 1000; $x++) {
  error_log("The number is: $x", 0);
  sleep(1);
}

exit();
