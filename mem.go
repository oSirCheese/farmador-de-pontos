package main

import "runtime/debug"

func freeGoMemory() { debug.FreeOSMemory() }
