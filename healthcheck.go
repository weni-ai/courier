package courier

import (
	"context"
	"fmt"
	"sync"
)

type HealthCheckResult struct {
	Status string
	Err    error
}

type HealthStatus struct {
	Status  string                       `json:"status,omitempty"`
	Message string                       `json:"message,omitempty"`
	Details map[string]map[string]string `json:"details,omitempty"`
}

type HealthCheck struct {
	wg              *sync.WaitGroup
	HealthStatus    *HealthStatus
	ComponentChecks []*ComponentCheck
}

type ComponentCheck struct {
	name          string
	checkFunction func() error
	result        *HealthCheckResult
}

func NewHealthCheck() *HealthCheck {
	return &HealthCheck{
		wg: &sync.WaitGroup{},
		HealthStatus: &HealthStatus{
			Details: map[string]map[string]string{},
		},
		ComponentChecks: []*ComponentCheck{},
	}
}

func (hc *HealthCheck) AddCheck(componentName string, checkFunction func() error) {
	component := &ComponentCheck{
		name:          componentName,
		checkFunction: checkFunction,
		result:        &HealthCheckResult{},
	}
	hc.ComponentChecks = append(hc.ComponentChecks, component)
}

func (hc *HealthCheck) CheckUp(ctx context.Context) {
	done := make(chan bool)

	totalComponents := len(hc.ComponentChecks)
	hc.wg.Add(totalComponents)

	hc.HealthStatus.Status = "Ok"
	hc.HealthStatus.Message = "All working fine!"
	errorsMsgs := ""

	for _, c := range hc.ComponentChecks {
		// go CheckHealthComponent(hc.wg, c)
		go c.CheckComponent(hc.wg)
	}
	go func() {
		hc.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		hc.HealthStatus.Status = "Timed out"
		hc.HealthStatus.Message = "Wait for check is too long. Health Check is timed out."
	}

	for _, c := range hc.ComponentChecks {
		resultMsg := fmt.Sprintf("%s ok", c.name)
		if c.result.Status != "Ok" {
			if c.result.Err == nil {
				c.result.Status = "Timed Out"
				c.result.Err = fmt.Errorf("%s check is timed out", c.name)
			}
		}
		if c.result.Err != nil {
			resultMsg = c.result.Err.Error()
			errorsMsgs = errorsMsgs + ", " + c.result.Err.Error()
		}

		hc.HealthStatus.Details[c.name] = map[string]string{
			"status":  c.result.Status,
			"message": resultMsg,
		}
	}

	if errorsMsgs != "" {
		hc.HealthStatus.Status = "Error"
		hc.HealthStatus.Message = errorsMsgs[2:]
	}
}

func (c *ComponentCheck) CheckComponent(wg *sync.WaitGroup) {
	defer wg.Done()
	if err := c.checkFunction(); err != nil {
		c.result.Status = "Error"
		c.result.Err = err
		return
	}
	c.result.Status = "Ok"
	c.result.Err = nil
}

func CheckHealthComponent(wg *sync.WaitGroup, c *ComponentCheck) {
	defer wg.Done()
	if err := c.checkFunction(); err != nil {
		c.result.Status = "Error"
		c.result.Err = err
		return
	}
	c.result.Status = "Ok"
	c.result.Err = nil
}

func CheckComponent(componentName string, wg *sync.WaitGroup, checkFunc func() error, results map[string]HealthCheckResult) {
	defer wg.Done()
	results[componentName] = HealthCheckResult{Status: "Timed out", Err: nil}
	if err := checkFunc(); err != nil {
		results[componentName] = HealthCheckResult{Status: "Error", Err: err}
		return
	}
	results[componentName] = HealthCheckResult{Status: "Ok", Err: nil}
}
