package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	"gopkg.in/yaml.v2"
)

type ESXiConfig struct {
	Environment struct {
		Vcenter struct {
			Hostname     string `yaml:"hostname"`
			Username     string `yaml:"username"`
			Password     string `yaml:"password"`
			Datacenter   string `yaml:"datacenter"`
			ResourcePool string `yaml:"resourcepool"`
			Folder       string `yaml:"folder"`
		} `yaml:"vcenter"`
		KickstartServer string `yaml:"kickstartserver"`
		BootPortGroup   string `yaml:"bootportgroup"`
	} `yaml:"environment"`
	EsxiInfo struct {
		Replica     int      `yaml:"replica"`
		StartIP     string   `yaml:"start_ip"`
		Netmask     string   `yaml:"netmask"`
		Gateway     string   `yaml:"gateway"`
		NamePrefix  string   `yaml:"name_prefix"`
		Domain      string   `yaml:"domain"`
		Password    string   `yaml:"password"`
		Nameserver  string   `yaml:"nameserver"`
		Vlanid      int      `yaml:"vlanid"`
		Keyboard    string   `yaml:"keyboard"`
		Isofilename string   `yaml:"isofilename"`
		Cli         []string `yaml:"cli"`
		NotVmPgCreate bool `yaml:"notvmpgcreate"`
	} `yaml:"esxiInfo"`
	VmParameter struct {
		Cpu struct {
			Core          int32 `yaml:"core"`
			CorePerSocket int32 `yaml:"coreperscket"`
		} `yaml:"cpu"`
		Memory struct {
			MemoryGB int64 `yaml:"memoryGB"`
		} `yaml:"memory"`
		Networks []string `yaml:"networks"`
		Storages []struct {
			Datastore  string `yaml:"datastore"`
			CapacityGB int64  `yaml:"capacityGB"`
		} `yaml:"storages"`
		BootOption struct {
			Firmware   string `yaml:"firmware"`
			SecureBoot bool   `yaml:"secureboot"`
		} `yaml:"bootoption"`
	} `yaml:"vmparameter"`
}

type RequestBody struct {
	Macaddress  string   `json:"macaddress"`
	Password    string   `json:"password"`
	Hostname    string   `json:"hostname"`
	IP          string   `json:"ip"`
	Netmask     string   `json:"netmask"`
	Gateway     string   `json:"gateway"`
	Nameserver  string   `json:"nameserver"`
	Vlanid      int      `json:"vlanid"`
	Keyboard    string   `json:"keyboard"`
	Isofilename string   `json:"isofilename"`
	Cli         []string `json:"cli"`
	NotVmPgCreate bool `json:"notvmpgcreate"`
}

type EsxiVersionResponse struct {
	UploadedFiles map[string]string `json:"uploaded_esxi_list"`
}

func calcurateNamePrefix(namePrefix string) (int, string) {
	parts := strings.Split(namePrefix, "{")
	prefix := parts[0]

	parts = strings.Split(parts[1], "}")
	formatDetails := parts[0]

	parts = strings.Split(formatDetails, ",")
	n, err := strconv.Atoi(parts[0])
	if err != nil {
		log.Fatalf("Error could not convert n to integer. Please confirm the name_prefix field of yaml file.: %s\n", err)
	}
	padding := 0
	if len(parts) > 1 {
		paddingStr := strings.Split(parts[1], "=")[1]
		padding, err = strconv.Atoi(paddingStr)
		if err != nil {
			log.Fatalf("Error could not convert fixed=n to integer. Please confirm the name_prefix field of yaml file.: %s\n", err)
		}
	}

	return n, prefix + fmt.Sprintf("%%0%dd", padding)
}

func vmCreateHandler(ctx context.Context, hostname string, ip net.IP, esxiconfig ESXiConfig, wg *sync.WaitGroup, changemac bool) {
	defer wg.Done()
	environment := esxiconfig.Environment
	vcInfo := environment.Vcenter
	vmParam := esxiconfig.VmParameter
	esxiInfo := esxiconfig.EsxiInfo

	kickstartserver := environment.KickstartServer

	vcUrl, err := url.Parse(fmt.Sprintf("https://%s:%s@%s/sdk", vcInfo.Username, vcInfo.Password, vcInfo.Hostname))
	si, err := govmomi.NewClient(ctx, vcUrl, true)
	if err != nil {
		log.Fatalf("Error Connect to vCenter: %s\n", err)
	}
	finder := find.NewFinder(si.Client, true)
	dc, err := finder.Datacenter(ctx, vcInfo.Datacenter)
	if err != nil {
		fmt.Println(err)
		return
	}

	finder.SetDatacenter(dc)

	_, err = finder.VirtualMachine(ctx, hostname)
	if err != nil {
		if _, ok := err.(*find.NotFoundError); ok {
			log.Printf("%s is not exsisting. The create tasks will be started.\n", hostname)
		} else {
			fmt.Println(err)
			return
		}
	} else {
		log.Printf("%s is exsisting. The create tasks will be skipped.\n", hostname)
		return
	}

	folders, err := dc.Folders(ctx)
	if err != nil {
		fmt.Println(err)
		return
	}
	getvmfolder := filepath.Join(folders.VmFolder.InventoryPath, vcInfo.Folder)
	vmfolder := filepath.ToSlash(getvmfolder)

	targetFolder, err := finder.Folder(ctx, vmfolder)
	if err != nil {
		fmt.Println(err)
		return
	}

	targetResourcePool, err := finder.ResourcePool(ctx, vcInfo.ResourcePool)
	if err != nil {
		fmt.Println(err)
		return
	}

	guestId, err := decideGuestId(kickstartserver, esxiInfo.Isofilename)
	if err != nil {
		fmt.Println(err)
		return
	}

	configSpec := &types.VirtualMachineConfigSpec{
		Name:              hostname,
		GuestId:           guestId,
		NumCPUs:           vmParam.Cpu.Core,
		NumCoresPerSocket: vmParam.Cpu.CorePerSocket,
		NestedHVEnabled:   types.NewBool(true),
		MemoryMB:          vmParam.Memory.MemoryGB * 1024,
		Files: &types.VirtualMachineFileInfo{
			VmPathName: fmt.Sprintf("[%s]", vmParam.Storages[0].Datastore),
		},
	}

	bootoption := vmParam.BootOption
	switch bootoption.Firmware {
	case "efi":
		if bootoption.SecureBoot {
			configSpec.BootOptions = &types.VirtualMachineBootOptions{
				EfiSecureBootEnabled: types.NewBool(true),
			}
		}
		configSpec.Firmware = "efi"
	case "bios":
		configSpec.Firmware = "bios"
	case "http-efi":
		if bootoption.SecureBoot {
			configSpec.BootOptions = &types.VirtualMachineBootOptions{
				EfiSecureBootEnabled: types.NewBool(true),
			}
		}
		configSpec.ExtraConfig = []types.BaseOptionValue{
			&types.OptionValue{
				Key:   "networkBootProtocol",
				Value: "httpv4",
			},
		}
		configSpec.Firmware = "efi"
	}
	devices := object.VirtualDeviceList{}
	devices, scsictlKey := createScsiController(devices)
	for i, datastore := range vmParam.Storages {
		devices = createVirtualDisk(i, devices, datastore.CapacityGB, hostname, scsictlKey, datastore.Datastore)
	}
	for i, network := range vmParam.Networks {
		if i == 0 {
			network = environment.BootPortGroup
		}
		net, err := finder.Network(ctx, network)
		if err != nil {
			log.Fatalf("Error Could not found the target network: %s\n", err)
		}
		networkBacking, err := net.EthernetCardBackingInfo(ctx)
		if err != nil {
			log.Fatalf("Error Could not get networking backing info: %s\n", err)
		}
		devices = createNetwork(devices, networkBacking)
	}

	deviceChange, err := devices.ConfigSpec(types.VirtualDeviceConfigSpecOperationAdd)
	if err != nil {
		log.Fatalf("Failed to create ConfigSpec: %v", err)
	}
	configSpec.DeviceChange = deviceChange
	task, err := targetFolder.CreateVM(ctx, *configSpec, targetResourcePool, nil)
	if err != nil {
		log.Fatalf("Failed to create VM: %v", err)
		return
	}

	taskInfo, err := task.WaitForResult(ctx, nil)
	if err != nil {
		log.Fatalf("Failed to create VM: %v", err)
		return
	}

	log.Printf("VM %s Created.\n", hostname)

	newVM := object.NewVirtualMachine(si.Client, taskInfo.Result.(types.ManagedObjectReference))

	var newVMProps mo.VirtualMachine
	err = newVM.Properties(ctx, newVM.Reference(), []string{"config.hardware.device"}, &newVMProps)
	if err != nil {
		log.Fatalf("Failed get object fot mac address: %v", err)
		return
	}
	var data RequestBody
	var bootNet *types.VirtualVmxnet3
	for _, device := range newVMProps.Config.Hardware.Device {
		switch device := device.(type) {
		case *types.VirtualVmxnet3:
			devinfo := device.DeviceInfo.(*types.Description)
			if devinfo.Label == "Network adapter 1" {
				bootNet = device
				data = RequestBody{
					Macaddress:  device.MacAddress,
					Password:    esxiInfo.Password,
					Hostname:    hostname,
					IP:          ip.String(),
					Netmask:     esxiInfo.Netmask,
					Gateway:     esxiInfo.Gateway,
					Nameserver:  esxiInfo.Nameserver,
					Vlanid:      esxiInfo.Vlanid,
					Keyboard:    esxiInfo.Keyboard,
					Isofilename: esxiInfo.Isofilename,
					Cli:         esxiInfo.Cli,
				}
				break
			}
		}
	}
	if data.Macaddress == "" {
		log.Fatalf("Could not found mac address of boot network: %v", err)
	}
	sendApiRequest(kickstartserver, "POST", data)

	task, err = newVM.PowerOn(ctx)
	if err != nil {
		log.Fatalf("Failed to start VM: %v", err)
		return
	}

	_, err = task.WaitForResult(ctx, nil)
	if err != nil {
		log.Fatalf("Failed to wait power on VM: %v", err)
		return
	}

	log.Printf("VM %s powered on.\n", hostname)

	err = waitForIP(ctx, newVM, ip.String(), hostname)
	if err != nil {
		log.Fatalf("Failed to wait for VM IP: %v", err)
		return
	}

	newNet, err := finder.Network(ctx, vmParam.Networks[0])
	bootNet.Backing, err = newNet.EthernetCardBackingInfo(ctx)
	if changemac {
		log.Printf("Change mac task for %s is started.", hostname)
		log.Printf("Shutting down %s.", hostname)
		err = newVM.ShutdownGuest(ctx)
		if err != nil {
			log.Fatalf("Failed to shutting down VM: %v", err)
			return
		}
		for {
			var vm mo.VirtualMachine
			err = si.RetrieveOne(ctx, newVM.Reference(), []string{"summary.runtime.powerState"}, &vm)
            if err != nil {
				log.Fatalf("Failed to retrieve %s info: %v", hostname, err)
                return
            }
			if vm.Summary.Runtime.PowerState == types.VirtualMachinePowerStatePoweredOff {
				log.Printf("%s is powered off.", hostname)
				break
			}
			time.Sleep(10)
		}

        log.Printf("Remove boot vnic for %s.", hostname)
		netConfigSpec := &types.VirtualMachineConfigSpec{
			DeviceChange: []types.BaseVirtualDeviceConfigSpec{
				&types.VirtualDeviceConfigSpec{
					Operation: types.VirtualDeviceConfigSpecOperationRemove,
					Device:    bootNet,
				},
			},
		}

		task, _ = newVM.Reconfigure(ctx, *netConfigSpec)
		if err != nil {
			log.Fatalf("Failed to change portgroup for boot network.: %v", err)
			return
		}

		_, err = task.WaitForResult(ctx, nil)
		if err != nil {
			log.Fatalf("Failed to change portgroup for boot network.: %v", err)
			return
		}

		log.Printf("Add new vnic for %s.", hostname)
		bootNet.MacAddress = ""
		netConfigSpec = &types.VirtualMachineConfigSpec{
			DeviceChange: []types.BaseVirtualDeviceConfigSpec{
				&types.VirtualDeviceConfigSpec{
					Operation: types.VirtualDeviceConfigSpecOperationAdd,
					Device:    bootNet,
				},
			},
		}

		task, _ = newVM.Reconfigure(ctx, *netConfigSpec)
		if err != nil {
			log.Fatalf("Failed to change portgroup for boot network.: %v", err)
			return
		}
        
		_, err = task.WaitForResult(ctx, nil)
		if err != nil {
			log.Fatalf("Failed to change portgroup for boot network.: %v", err)
			return
		}
        
		log.Printf("Powering on %s.", hostname)
		task, err = newVM.PowerOn(ctx)
		if err != nil {
			log.Fatalf("Failed to power off VM: %v", err)
			return
		}

		err = waitForIP(ctx, newVM, ip.String(), hostname)
		if err != nil {
			log.Fatalf("Failed to wait for VM IP: %v", err)
			return
		}

	} else {
		netConfigSpec := &types.VirtualMachineConfigSpec{
			DeviceChange: []types.BaseVirtualDeviceConfigSpec{
				&types.VirtualDeviceConfigSpec{
					Operation: types.VirtualDeviceConfigSpecOperationEdit,
					Device:    bootNet,
				},
			},
		}
		task, _ = newVM.Reconfigure(ctx, *netConfigSpec)
		if err != nil {
			log.Fatalf("Failed to change portgroup for boot network.: %v", err)
			return
		}
		_, err = task.WaitForResult(ctx, nil)
		if err != nil {
			log.Fatalf("Failed to change portgroup for boot network.: %v", err)
			return
		}
		log.Printf("The network adapter port group for %s has been changed successfully.", hostname)
	}

	sendApiRequest(kickstartserver, "DELETE", data)

	log.Printf("Installation for %s has been completed.", hostname)
}

func decideGuestId(url, isofilename string) (string, error) {
	var resp *http.Response
	rootPath := "/esxi-versions"
	resp, err := http.Get(url + rootPath)
	if err != nil {
		log.Fatalf("Error: failed to send API request: %s\n", err)
		return "", err
	}
	defer resp.Body.Close()
	var versions EsxiVersionResponse
	err = json.NewDecoder(resp.Body).Decode(&versions)
	if err != nil {
		log.Fatalf("Error: failed to decode response body: %s\n", err)
		return "", err
	}
	var esxiVersion string
	for filename, version := range versions.UploadedFiles {
		if filename == isofilename {
			esxiVersion = version
		}
	}
	var guestId string
	switch esxiVersion {
	case "":
		log.Fatalf("Error: Iso file could not found on Kickstart Server.")
	case "6.0.0":
		guestId = "vmkernel6Guest"
	case "6.5.0", "6.7.0":
		guestId = "vmkernel65Guest"
	default:
		guestId = "vmkernel7Guest"
	}
	return guestId, nil
}

func waitForIP(ctx context.Context, vm *object.VirtualMachine, targetIP string, hostname string) error {
	ticker := time.NewTicker(time.Second * 60)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			var mo mo.VirtualMachine
			err := vm.Properties(ctx, vm.Reference(), []string{"guest"}, &mo)
			if err != nil {
				return err
			}

			if mo.Guest.IpAddress == targetIP {
				log.Printf("IP address for %s is expected %s.", hostname, mo.Guest.IpAddress)
				return nil
			} else {
				var currentIP string
				if mo.Guest.IpAddress == "" {
					currentIP = "null"
				} else {
					currentIP = mo.Guest.IpAddress
				}
				log.Printf("Check to Install status for %s. Current ip address is %s.", hostname, currentIP)
			}
		}
	}
}

func sendApiRequest(url, method string, reqBody RequestBody) {
	var resp *http.Response
	rootPath := "/ks"
	switch method {
	case "POST":
		contentType := "application/json"
		requestBody, err := json.Marshal(reqBody)
		if err != nil {
			log.Fatalf("Error: failed to marshal request body: %s\n", err)
			return
		}
		resp, err = http.Post(url+rootPath, contentType, bytes.NewBuffer(requestBody))
		if err != nil {
			log.Fatalf("Error: failed to send API request: %s\n", err)
			return
		}
		defer resp.Body.Close()
		log.Printf("Sent %s request to kickstart config for %s. Response: %s\n", method, reqBody.Hostname, resp.Status)
	case "DELETE":
		subPath := strings.Replace(reqBody.Macaddress, ":", "-", -1)
		getDeletePath := filepath.Join(rootPath, subPath)
		deletePath := filepath.ToSlash(getDeletePath)
		client := &http.Client{}
		req, err := http.NewRequest("DELETE", fmt.Sprintf("%s%s", url, deletePath), nil)
		if err != nil {
			log.Fatalf("Error: failed to send API request: %s\n", err)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			log.Fatalf("Error: failed to send API request: %s\n", err)
			return
		}
		defer resp.Body.Close()
		log.Printf("Sent %s request to kickstart config for %s. Response: %s\n", method, reqBody.Hostname, resp.Status)
	default:
		log.Fatalf("Error: unsupported HTTP method: %s\n", method)
		return
	}
}

func createScsiController(devices object.VirtualDeviceList) (object.VirtualDeviceList, int32) {
	scsiController := &types.ParaVirtualSCSIController{
		VirtualSCSIController: types.VirtualSCSIController{
			SharedBus: types.VirtualSCSISharingNoSharing,
			VirtualController: types.VirtualController{
				BusNumber: 0,
				VirtualDevice: types.VirtualDevice{
					Key: devices.NewKey(),
				},
			},
		},
	}
	devices = append(devices, scsiController)
	return devices, scsiController.Key
}

func createVirtualDisk(i int, devices object.VirtualDeviceList, diskSizeKB int64, vmName string, scsictlKey int32, datastore string) object.VirtualDeviceList {
	disk := &types.VirtualDisk{
		VirtualDevice: types.VirtualDevice{
			Key:        devices.NewKey(),
			UnitNumber: new(int32),
			Backing: &types.VirtualDiskFlatVer2BackingInfo{
				DiskMode:        string(types.VirtualDiskModePersistent),
				ThinProvisioned: types.NewBool(true),
			},
		},
		CapacityInKB: diskSizeKB * 1024 * 1024,
	}
	*disk.UnitNumber = int32(i)
	if i >= 7 {
		*disk.UnitNumber++
	}
	var filename string
	if i == 0 {
		filename = fmt.Sprintf("[%s] %s/%s.vmdk", datastore, vmName, vmName)
	} else {
		filename = fmt.Sprintf("[%s] %s/%s_%d.vmdk", datastore, vmName, vmName, i)
	}
	diskControllerKey := scsictlKey
	disk.ControllerKey = diskControllerKey
	disk.Backing = &types.VirtualDiskFlatVer2BackingInfo{
		VirtualDeviceFileBackingInfo: types.VirtualDeviceFileBackingInfo{
			FileName: filename,
		},
		DiskMode:        string(types.VirtualDiskModePersistent),
		ThinProvisioned: types.NewBool(true),
	}
	devices = append(devices, disk)
	return devices
}

func createNetwork(devices object.VirtualDeviceList, networkBacking types.BaseVirtualDeviceBackingInfo) object.VirtualDeviceList {
	netDevice := &types.VirtualVmxnet3{
		VirtualVmxnet: types.VirtualVmxnet{
			VirtualEthernetCard: types.VirtualEthernetCard{
				VirtualDevice: types.VirtualDevice{
					Key:     devices.NewKey(),
					Backing: networkBacking,
				},
			},
		},
	}
	devices = append(devices, netDevice)
	return devices
}

func validateNetworkAddr(esxiconfig ESXiConfig) error {
	esxiInfo := esxiconfig.EsxiInfo
	startIPAddr := net.ParseIP(esxiInfo.StartIP)
	netmaskAddr := net.IPMask(net.ParseIP(esxiInfo.Netmask).To4())
	gatewayAddr := net.ParseIP(esxiInfo.Gateway)
	network := startIPAddr.Mask(netmaskAddr)
	gwnetwork := gatewayAddr.Mask(netmaskAddr)

	if gwnetwork.Equal(network) {
		return nil
	} else {
		return errors.New("Gateway and ESXi's IP are not on same subnet.")
	}
	return nil
}

func main() {
	sigChannel := make(chan os.Signal, 1)
	signal.Notify(sigChannel, os.Interrupt)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	yamlPath := flag.String("yaml", "template.yaml", "path to the yaml config file")
	changemac := flag.Bool("changemac", false, "separate mac address vmk0 and vmnic0")
	flag.Parse()

	yamlFile, err := ioutil.ReadFile(*yamlPath)
	if err != nil {
		log.Fatalf("Error: reading YAML file: %s\n", err)
	}

	var esxiconfig ESXiConfig
	err = yaml.Unmarshal(yamlFile, &esxiconfig)
	if err != nil {
		log.Fatalf("Error: parsing YAML file: %s\n", err)
	}

	err = validateNetworkAddr(esxiconfig)
	if err != nil {
		log.Fatalf("Error: Validate IP Address: %s\n", err)
	}
	startIP := net.ParseIP(esxiconfig.EsxiInfo.StartIP).To4()
	n, hostNamePrefix := calcurateNamePrefix(esxiconfig.EsxiInfo.NamePrefix)
	var wg sync.WaitGroup
	for i, j := 0, n; i < esxiconfig.EsxiInfo.Replica; i, j = i+1, j+1 {
		wg.Add(1)
		hostname := fmt.Sprintf(hostNamePrefix+"."+esxiconfig.EsxiInfo.Domain, j)
		ip := make(net.IP, len(startIP))
		copy(ip, startIP)
		ip[3] += byte(i)
		go vmCreateHandler(ctx, hostname, ip, esxiconfig, &wg, *changemac)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Printf("All installation tasks has been completed.")
	case <-sigChannel:
		log.Printf("Received an interrupt signal. All tasks canceled.")
	}
}
