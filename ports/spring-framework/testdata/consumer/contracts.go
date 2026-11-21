package consumer

import (
	spring "github.com/StevenBuglione/spring-framework-gpp/org/springframework/core"
	"github.com/StevenBuglione/spring-framework-gpp/org/springframework/util/backoff"
)

var (
	_ spring.Ordered         = orderedBean(0)
	_ spring.PriorityOrdered = priorityBean{}
	_ backoff.BackOff        = (*backoff.FixedBackOff)(nil)
)

type orderedBean int32

func (bean orderedBean) GetOrder() int32 {
	return int32(bean)
}

type priorityBean struct {
	orderedBean
}

func (priorityBean) IsPriorityOrdered() {}
