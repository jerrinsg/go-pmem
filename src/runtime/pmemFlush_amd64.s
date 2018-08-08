// +build amd64

TEXT runtime·sfence(SB),$0
	SFENCE
	RET

TEXT runtime·clflush(SB), $0-8
	MOVQ 	ptr+0(FP), BX
	// clflush BX
	BYTE $0x0F; BYTE $0xAE; BYTE $0x3B;
	RET

TEXT runtime·clwb(SB), $0-8
	MOVQ 	ptr+0(FP), BX
	// clwb BX
	BYTE $0x66; BYTE $0x0F; BYTE $0xAE; BYTE $0x33;
	RET

TEXT runtime·clflushopt(SB), $0-8
	MOVQ 	ptr+0(FP), BX
	// clflushopt BX
	BYTE $0x66; BYTE $0x0F; BYTE $0xAE; BYTE $0x3B;
	RET
