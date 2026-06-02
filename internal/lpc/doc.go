// Package lpc implements FLAC's linear prediction: the fixed predictors
// (orders 0 to 4) and the full quantized-LPC path (windowing, autocorrelation,
// Levinson-Durbin, coefficient quantization). It provides both residual
// computation (encode) and signal restoration (decode). Restore lands in M2,
// analyze in M3.
package lpc
