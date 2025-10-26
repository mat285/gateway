package main

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
)

var (
	machines = []string{
		"0.ts.gateway.nori.ninja",
		"1.ts.gateway.nori.ninja",
	}

	version = "v0.6.1" // Update this to the latest version of your script

	commands = []string{
		`sh -c "$(curl -fsSL https://github.com/mat285/gateway/releases/download/` + version + `/install.sh)"`,
	}
)

func main() {
	wg := &sync.WaitGroup{}
	lock := &sync.Mutex{}
	for _, machine := range machines {
		wg.Add(1)
		func(machine string) {
			defer wg.Done()
			lock.Lock()
			fmt.Println("Setting up machine:", machine)
			lock.Unlock()
			for _, cmdStr := range commands {
				lock.Lock()
				fmt.Println("Running cmd:", cmdStr, "for machine:", machine)
				lock.Unlock()
				cmd := exec.Command("ssh",
					"-A",
					machine,
					"-T",
					cmdStr,
				)
				cmd.Env = append(os.Environ(), `SUDO_OPTS="-S"`)
				output, err := cmd.CombinedOutput()
				lock.Lock()
				fmt.Println(string(output))
				lock.Unlock()
				if err != nil {
					fmt.Printf("Error running command on %s: %v\n", machine, err)
					continue
				}
			}
			lock.Lock()
			fmt.Printf("Successfully updated %s\n", machine)
			lock.Unlock()
		}(machine)
	}
	wg.Wait()
}
