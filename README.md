# Automated Installation Tool for Nested ESXi with Kickstart Client

## Overview
This tool is the client for the vmware-esxi-kickstart-server.  
https://github.com/makgol/vmware-esxi-kickstart-server

## Functionality
When the code is executed, Nested ESXi will be automatically created in coordination with the server.

## Yaml Variables
| Category        | Value                | Type     | Description                                                          |
| --------------- | -------------------- | -------- | -------------------------------------------------------------------- |
| **environment** | `vcenter`            |          |                                                                      |
|                 | &emsp;`hostname`     | `string` | vcenter fqdn or ip                                                   |
|                 | &emsp;`username`     | `string` | vcenter username                                                     |
|                 | &emsp;`password`     | `string` | vcenter password                                                     |
|                 | &emsp;`datacenter`   | `string` | target datacenter                                                    |
|                 | &emsp;`resourcepool` | `string` | target resourcepool                                                  |
|                 | &emsp;`folder`       | `string` | target folder. It does not support recursive search of folders. Enter a path that can be seen by the vSpehre client. e.g. "Discovered virtual machine/automated-vms"|
|                 | &emsp;`kickstartserver`   | `string` | url of nested-esxi-auto-installer server's api interface. e.g. http://<nested-esxi-auto-installer_ip>:80 |
|                 | `bootportgroup`      | `string` | service(dhcp, tftp, api) port of nested-esxi-auto-installer           |
| **esxiInfo**    | `replica`            | `integer`| number of nested esxi                                                |
|                 | `start_ip`           | `string` | start ip for nested esxi                                             |
|                 | `netmask`            | `string` | netmask for nested esxi                                              |
|                 | `gateway`            | `string` | gateway for nested esxi                                              |
|                 | `name_prefix`        | `string` | name prefix for nested esxi. supported format name{n} or name{n,fixed=n}. if you set esxi{1, fixed=3}, first esxi name is esxi001. |
|                 | `domain`             | `string` | domain for nested esxi                                               |
|                 | `password`           | `string` | root password for nested esxi                                        |
|                 | `nameserver`         | `string` | nameserver for nested esxi                                           |
|                 | `vlanid`             | `integer`| vlanid for nested esxi                                               |
|                 | `keyboard`           | `string` | keyboard type for nested esxi                                        |
|                 | `isofilename`        | `string` | installer iso file name                                              |
|                 | `cli`                | `array`  |                                                                      |
|                 |                      | `string` | cli commands after install. if you use secure boot, commands do not work |
| **vmparameter** | `cpu`                |          |                                                                      |
|                 | &emsp;`core`         | `integer`| cpu core                                                             |
|                 | &emsp;`coreperscket` | `integer`| core per sockets                                                     |
|                 | `memory`             |          |                                                                      |
|                 | &emsp;`memoryGB`     | `integer`| memory size GB                                                       |
|                 | `networks`           | `array`  |                                                                      |
|                 |                      | `string` | portgroup name(vss and vds are supported)                            |
|                 | `storages`           | `array`  |                                                                      |
|                 | &emsp;`datastore`    | `string` | datastore name                                                       |
|                 | &emsp;`capacityGB`   | `integer`| datastore name and capacity in GB                                    |
|                 | `bootoption`         |          |                                                                     
|                 | &emsp;`firmware` | `string`| Firmware type (bios or efi or http-efi. http-efi is supported when the parent vSphere is 7.0u2 or later.)                                     |
|                 | &emsp;`secureboot`| `bool`  | Secure boot option (secure boot is supported with only http-efi)     |



## Usage
1. Edit the template.yaml according to your environment.
2. Execute the code.
    ```bash
    go run main.go
    ```
3. If you use an another yaml file, you can use -yaml option.
    ```bash
    go run main.go -yaml <your yaml path>
    ```

## Options
| Option | Type | Default Value | Description |
| --- | --- | --- | --- |
| `yaml` | `string` | `template.yaml` | Please specify the yaml file if you use other template file instead of template.yaml. |
| `changemac` | `bool` | `false` | The MAC addresses of vmk0 and vmnic0 will be set to different values. This can be useful in cases such as VCF, where vmk0 is being migrated to VDS, when the parent vSphere is using only MAC learning in its port group. |