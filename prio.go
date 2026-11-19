//go:build windows

package main

import "unsafe"

var (
	pSetPriorityClass      = kernel32.NewProc("SetPriorityClass")
	pSetProcessInformation = kernel32.NewProc("SetProcessInformation")
)

// fullSpeed desliga o "modo eficiência"/EcoQoS do Windows para o app e sobe a
// prioridade para Acima do Normal. Assim, minimizado, ele não é desacelerado
// pelo Windows. Não muda nada no uso de memória.
func fullSpeed() {
	proc, _, _ := pGetCurrentProcess.Call()
	pSetPriorityClass.Call(proc, 0x8000) // ABOVE_NORMAL_PRIORITY_CLASS

	if pSetProcessInformation.Find() != nil { // Windows 7/8: não existe EcoQoS
		return
	}
	// PROCESS_POWER_THROTTLING_STATE: controlar EXECUTION_SPEED e
	// IGNORE_TIMER_RESOLUTION com estado 0 = nunca desacelerar.
	st := struct{ Version, ControlMask, StateMask uint32 }{1, 0x1 | 0x4, 0}
	pSetProcessInformation.Call(proc, 4 /*ProcessPowerThrottling*/, uintptr(unsafe.Pointer(&st)), unsafe.Sizeof(st))
}
