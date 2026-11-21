module example.com/gpp-interop-consumer

go 1.27

require (
	example.com/gpp-interop-library v0.0.0
	example.com/gpp-spring v0.0.0
)

replace (
	example.com/gpp-interop-library => ../generated
	example.com/gpp-spring => ../spring
)
