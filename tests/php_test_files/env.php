<?php
// The config maps foo -> BAR and bar -> FOO; the plugin uppercases the keys.
if ($_SERVER["FOO"] !== "BAR") {
	die("faillll");
}

if ($_SERVER["BAR"] !== "FOO") {
	die("faillll");
}

error_log("env ok", 0);
exit();
