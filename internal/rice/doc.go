// Package rice implements FLAC's Rice/Golomb residual coding: encoding and
// decoding of partitioned residuals and the parameter search that picks the
// cheapest Rice parameter per partition. Decode lands in M2, encode in M3.
package rice
