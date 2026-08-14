<?php
// Writes to stdout, and echoes the env entries the config passes in.
for ($x = 0; $x <= 6; $x++) {
  fwrite(STDOUT, "stdout write FOO=" . getenv('FOO') . " FOO2=" . getenv('FOO2'));
  sleep(1);
}

exit();
