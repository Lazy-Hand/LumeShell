package sshclient

import "os"

// osReadFile is only here so tests can stand in for the file system.
var osReadFile = os.ReadFile
