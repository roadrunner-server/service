<?php

fwrite(STDOUT, json_encode(getenv(), JSON_THROW_ON_ERROR) . "\n");
sleep(1000);
