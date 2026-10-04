<?php

pcntl_async_signals(true);
pcntl_signal(SIGINT, static function (): void {
    fwrite(STDOUT, "interrupt\n");
});

fwrite(STDOUT, "ready VALUE=" . getenv('VALUE') . "\n");
while (true) {
    sleep(1);
}
