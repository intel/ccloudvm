# Configurable Cloud VM (ccloudvm)

[![Go Report Card](https://goreportcard.com/badge/github.com/intel/ccloudvm)](https://goreportcard.com/report/github.com/intel/ccloudvm)
[![Build Status](https://travis-ci.org/intel/ccloudvm.svg?branch=master)](https://travis-ci.org/intel/ccloudvm)
[![Coverage Status](https://coveralls.io/repos/github/intel/ccloudvm/badge.svg?branch=master)](https://coveralls.io/github/intel/ccloudvm?branch=master)

## Introduction

Configurable Cloud VM (ccloudvm) is a command line tool for creating and
managing Virtual Machine (VM) based development environments.  ccloudvm creates
these development environments from the cloud images of Linux distributions.
These images have a number of interesting properties.  They are small, quick to
download, quick to start and are configurable using a tool,
[cloud-init](https://cloudinit.readthedocs.io/en/latest/), widely adopted in the
cloud.  ccloudvm builds VM based development environments from either
pre-shipped or user supplied annotated cloud-init files.

All you need to have installed on your machine is:

- Go 1.8 or greater

The installation instructions for the latest version of Go can be
found [here](https://golang.org/doc/install).  Once installed, ensure
that your PATH environment variable contains the location of
executables built with the Go tool chain.  This can be done by
executing the following command.

```
$ export PATH=$PATH:$(go env GOPATH)/bin
```

Then, to create a new Ubuntu 16.04 VM, simply type

```
go get github.com/intel/ccloudvm
ccloudvm setup
ccloudvm create xenial
```

The go get command downloads, builds and installs ccloudvm.  The
ccloudvm setup command installs some needed dependencies on your local
PC such as qemu and xorriso, and initialises a systemd user service.
The ccloudvm create command downloads an Ubuntu Cloud Image and
creates a VM based on this image.  It then boots the VM, creates an
account for you, with the same name as your account on your host, and
optionally updates the default packages.

ccloudvm will cache the Ubuntu image locally so the next time you
create another xenial based VM you won't have to wait very long.
It also knows about HTTP proxies and will mirror your host computer's proxy
settings inside the VMs it creates.

Once it's finished you'll be able to connect to the the VM via SSH
using the following command.

```
ccloudvm connect
```

The command above assumes that either $GOPATH/bin or ~/go/bin is in
your PATH.

You'll notice that the instance was assigned a name when it was created.  This
name is used refer to the instance when issuing future ccloudvm commands.  You
have the option to specify a name when creating an instance, using the --name
option.  If you do not provide a name, ccloudvm will create one for you.
Assuming the name of our new instance was 'vague-nimue' the above connect
command could have been written as.

```
ccloudvm connect vague-nimue
```

If there is only one ccloudvm instance in existence the name is optional and
need not be specified when issuing most commands.  However, if you have created
two or more instances you will need to specify an instance name when issuing
ccloudvm commands so ccloudvm knows which instance to operate on.

You can delete the VM you've just created by running,

```
ccloudvm delete
```

## Workloads

Each VM is created from a workload. A workload is a text file which contains
a set of instructions for initialising the VM.  A workload, among other things,
can specify:

- The hostname of the VM.
- The resources to be consumed by the VM
- The base image from which the VM is to be created
- The folders that should be shared between the host and the VM
- Any file backed storage that should appear as a device in the VM
- An annotated cloud-init file that contains the set of instructions
  to run on the first boot of the VM.  This file is used to create
  user accounts, install packages and configure the VM.

ccloudvm ships with a number of workloads for creating VMs based on standard images,
such as Ubuntu 16.04 and Fedora 25.  Users are also free to create their own workloads.
Standard workloads are stored in $GOPATH/src/github.com/intel/ccloudvm/workloads.
User created workloads are stored in ~/.ccloudvm/workloads.  ccloudvm always checks the
~/.ccloudvm/workloads directory first so if a workload exists in both directories
with the same name, ccloudvm will use the workload in ~/.ccloudvm/workloads.

Which workload to use is specified when creating the VM with the create
command. The workload can be a file without the .yaml extension or a URI pointing to
a local or remote file. If the workload is a file without the .yaml extension then it
must be present in either of the two directories mentioned above. For example,
the create command in the introduction section creates an Ubuntu 16.04
workload (xenial).  This caused ccloudvm to load the workload definition in
$GOPATH/src/github.com/intel/ccloudvm/workloads/xenial.yaml.
In the case of a remote file the supported schemes are http, https and file. Be aware
of the remote file will not be saved hence each time this option is used, ccloudvm
will try to get it from the remote location. For example, to create a workload using the
file /home/x/workload.yaml we have two options.

Using the file scheme:

```
ccloudvm create file:///home/ccloudvm/workload.yaml
```

or its absolute path:

```
ccloudvm create  /home/ccloudvm/workload.yaml
```


As the majority of the ccloudvm workloads are cloud-init files, ccloudvm only
works with images that are designed to run cloud-init on their first boot.  Typically,
these are the cloud images, e.g.,. the [Ubuntu Cloud Images](https://cloud-images.ubuntu.com/).

## Creating new Workloads

ccloudvm workloads are multi-doc YAML files.  The first document contains invariants
about the VM created from the workload such as the image off which it is based and its
hostname.  The second contains dynamic instance data that can be altered on every boot,
such as the number of CPUs the VM uses and the ports mapped.  The final document contains
the cloud-init file.  If only one section is present it is assumed to the be cloud-init
file and default values are used for the other two sections.

An example workload is shown below:

```
---
base_image_url: https://cloud-images.ubuntu.com/xenial/current/xenial-server-cloudimg-amd64-disk1.img
base_image_name: Ubuntu 16.04
...
---
mem_mib: 2048
cpus: 2
...
---
#cloud-config
package_upgrade: false

users:
  - name: {{.User}}
    gecos: CIAO Demo User
    lock-passwd: true
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    ssh-authorized-keys:
    - {{.PublicKey}}
...
```

### Templates

[Go templates](https://golang.org/pkg/text/template/) can be used in each
document in a workload yaml file.  These templates have access to an instance
of a structure called Workspace that contains some useful information about the
host's environment and the instance that is being created.  The following pieces
of information are available via the workload structure.

 - GoPath         : The GoPath on the host or ~/go if GOPATH is not defined
 - Home           : The home directory of the user who ran ccloudvm
 - HTTPProxy      : The hosts HTTP proxy if set
 - HTTPSProxy     : The hosts HTTPS proxy if set
 - NoProxy        : The value of the host's no_proxy variable
 - User           : The user who invoked ccloudvm
 - PublicKey      : The public key that will be used to access the instance over SSH
 - GitUserName    : The git user name of the user who ran ccloudvm
 - GitEmail       : The git email address of the user who ran ccloudvm
 - Mounts         : A slice of mounts, each describing a path to be shared between guest and host
 - Hostname       : The instance hostname
 - UUID           : A UUID for the new instance
 - PackageUpgrade : Indicates whether package upgrade should be performed during the first boot.

As an example consider the third document in the workload definition above.  The User and
PublicKey fields are accessed via the {{.User}} and {{.PublicKey}} Go templates.  Abstracting
all of the user and instance specific information via templates in this way allows us to keep our
workload definitions generic.

### The Instance Specification Document

The first yaml document is called the instance specification document.  It defines the fixed
characteristics of instances created from the workload which cannot be altered.  Three
fields are currently defined:

- base_image_url  : The URL of the qcow2 image upon which instances of the workload should be based.
- base_image_name : Friendly name for the base image.  This is optional.
- hostname        : The hostname of instances created from this workload.  Hostname is also optional and defaults to singlevm if not provided.

### The Instance Data Document

The second document is called the instance data document.  It contains information that is
set when an instance is first created but may be modified at a later date, without deleting
the instance.    Four fields are currently defined:

- mem_mib : Number of mebibytes to assign to the VM.  Defaults to half the memory on your machine
- cpus    : Number of CPUs to assign to the VM.  Defaults to half the cores on your machine
- ports   : Slice of port objects which map host ports on 127.0.0.1 to guest ports
- mounts  : Slice of mount objects which describe the folders shared between the host and the guest
- drives  : Slice of drive objects which identify resources accessible on the host to be made available as block devices on the guest.

Each port object has two members, host and guest.  They are both integers and they specify
the mapping of port numbers from host to guest.  A default mapping of 10022 to 22 is always
added so that users can connect to their VMs via ccloudvm connect.  An example port
mapping is shown below:

```
ports:
- host: 10022
  guest: 22
```

Mount objects can be used to share folders between the host and guest.  Folders are shared
using the 9p protocol.  Each mount object has three pieces of information.

- tag            : An id for the mount.  This information is needed when mounting the shared folder in the guest
- security_model : The 9p security model to use
- path           : The path of the host folder to share

An example of a mount is given below.

```
mounts:
- tag: hostgo
  security_model: passthrough
  path: /home/user/go
```

Note that specifying a mount in the instance data document only creates a
9p device which is visible inside the guest.  To actually access the shared
folder from inside the guest you need to mount the folder.  This can be
done in the cloud-init file discussed below.

Drive objects allow the user to make a resource accessible from the host
available as a block device in the guest VM.   Currently, these resources
are restricted to being file backed storage located on the host.  Each
drive object has three pieces of information.

- path          : The path of the resource as accessed from the host
- format        : The format of the resource, e.g., qcow2
- options       : A comma separate list of options.  This field is optional.

An example of a drive is given below.

```
  drives:
  - path: /tmp/img.qcow2
    format: qcow2
    options: aio=native
```


### The Cloudinit document

The third document contains a cloud-init user data file that can be used
to create users, mount folders, install and update packages and perform
general configuration tasks on the newly created VM.  The user is
expected to be familiar with the
[cloudinit file format](https://cloudinit.readthedocs.io/en/latest/).

Like the instance specification and data documents, the cloudinit document
is processed by the Go template engine before being passed to cloudinit
running inside the guest VM.  The template engine makes a number of
functions available to the cloudinit document.  These functions are used
to perform common tasks such as configuring proxies and communicating with
ccloudvm running on the host.  Each function must be passed the
workspace object as the first parameter.  The following functions are
available:

#### proxyVars

Generates a string containing variable definitions for all the proxy
variables.  It is useful for prepending to commands inside the runcmd:
section of the cloudinit document that require proxies to be set, e.g,

```
- {{proxyvars .}} wget https://storage.googleapis.com/golang/go1.8.linux-amd64.tar.gz" "/tmp/go1.8.linux-amd64.tar.gz
```

may expand to something like after being processed by the template engine

```
- https_proxy=http://myproxy.mydomain.com:911 wget https://storage.googleapis.com/golang/go1.8.linux-amd64.tar.gz" "/tmp/go1.8.linux-amd64.tar.gz
```

#### proxyEnv

Generates proxy definitions in a format that is suitable for the /etc/environment file.
It should be used as follows:

```
write_files:
{{with proxyEnv . 5}}
 - content: |
{{.}}
   path: /etc/environment
{{end}}
```

#### download

Download should be used to download files from inside the guest.  Download is preferable
to directly downloading files inside the cloudinit document using curl or wget for
example, as the downloaded files are cached by ccloudvm.  This means that the next time
an instance of this workload is created the file will not have to be retrieved from
the Internet, resulting in quicker boot times for the new VM.

download takes three parameters

- The workspace object
- The URL of the file to download
- The location on the guest where the downloaded file should be stored

An example of its usage is

```
- {{download . "https://storage.googleapis.com/golang/go1.8.linux-amd64.tar.gz" "/tmp/go1.8.linux-amd64.tar.gz"}}
```

#### The task functions

There are four functions that can be used to provide visual information back to the user
who invoked ccloudvm on the host.  These functions should always be issued in pairs.
The first function invoked should always be beginTask.

beginTask takes two parameters.  The first is the workspace object.  The second is a
string to present to the user.  beginTask is typically followed by a command and
then a call to either endTaskOk, endTaskFail or endTaskCheck.

endTaskOk prints "[OK]" to the screen, endTaskFail prints "[FAIL]" to the screen and
endTaskCheck prints "[OK]" or "[FAIL]" depending on the result of the previous command.

For example,

```
 - {{beginTask . "Unpacking Go" }}
 - tar -C /usr/local -xzf /tmp/go1.8.linux-amd64.tar.gz
 - {{endTaskCheck .}}
```

will print

Unpacking Go : [OK]

if the tar command succeeds

and

Unpacking Go : [FAIL]

if it fails.

Reporting a failure back to ccloudvm does not cause the create command to exit.  The
failure is presented to the user and the setup of the VM continues.

### Automatically mounting shared folders

As previously mentioned mounts specified in the instance data document will only
create 9p devices in the guest.  In order to access the files in the shared folder
you need to arrange to have these devices mounted.  The way you do this might
differ from distro to distro.  On Ubuntu it is done by adding a mount: section
to the cloudinit document, e.g.,

```
mounts:
{{range .Mounts}} - [{{.Tag}}, {{.Path}}, 9p, "x-systemd.automount,x-systemd.device-timeout=10,nofail,trans=virtio,version=9p2000.L", "0", "0"]
{{end -}}
```

The above command will arrange for all mounts specified in the instance data document or on
the create command line to be mounted to the same location that they are mounted on the
host.

Mounts added later via the start command will need to be mounted manually.

## Commands

### create

ccloudvm create creates and configures a new ccloudvm VM.  All the files associated
with the VM are stored under the ~/.ccloudvm/instances folder.

An example of ccloudvm create is given below:

```
$ ccloudvm create xenial
Downloading Ubuntu 16.04
Downloaded 289 MB of 289
Booting VM with 1024 MiB RAM and 1 cpus
Booting VM : [OK]
Adding amused-lancelot to /etc/hosts : [OK]
VM successfully created!

Instance amused-lancelot created
Type ccloudvm connect amused-lancelot to start using it.
```

By default, ccloudvm will assign 1GiB of memory, 1 VCPU and 60Gib of disk space
to each new instance it creates.  These resource allocations can be overridden
both in the workload specification and on the command line using the --mem, --cpu,
and --disk options.  For example,

ccloudvm create --cpus 2 --mem 2048 --disk 10 xenial

Creates and boots a VM with 2 VCPUs, 2 GB of RAM and a rootfs of max 10 GiB.

The --package-upgrade option can be used to provide a hint to workloads
indicating whether packages contained within the base image should be updated or not
during the first boot.  Updating packages can be quite time consuming
and may not be necessary if the user just wants to create a throw away
VM to test something under a particular distro.  This hint only works if
the workloads define the following in their cloudinit documents.

```
package_upgrade: {{with .PackageUpgrade}}{{.}}{{else}}false{{end}}
```
The -qemuport option can be used to follow qemu boot logs by connecting to user defined port or an entry in the workload yaml
file.

For example,

--qemuport=9999

will let you track qemu logs by running the below command

nc localhost 9999

You can also allow a login via this port in case ssh fails to work by modifying the workload file and changing lock_passwd to false and providing a passwd: "....." entry following that.

#### Port mappings, Mounts and Drives

Each new instance created by ccloudvm is assigned a host IP address on
which guest services can be exposed to the host and in some cases the
outside world.  When creating a new instance you can specifiy the host
IP address on which to expose guest services using the --hostip
option.  However, unless you need those services to be accessible
externally, it's usually best to go with the default behaviour and let
ccloudvm create choose the host IP address for you.  If you do not specify
the --hostip option an IP address from the range 127.0.0.0/8 will be
allocated to your instance.

To expose a guest service you need to map a port on the host IP address assigned
to your instance to the port on which the service is listening on the guest.
By default, ccloudvm creates one port mapping for new VMs, 10022-22 for SSH
access.  You can specify additional port mappings, mounts or drives or even override
the default settings on the command line.

For example,

./ccloudvm create --mount docs,passthrough,$HOME/Documents --port 10000-80 xenial

shares the host directory, $HOME/Documents, with the ccloudvm VM using the 9p
passthrough security model.  The directory can be mounted inside the VM using
the docs tag.  The command also adds a new port mapping.  HOSTIP:10000 on
the host now maps to port 80 on the guest, where HOSTIP is the host IP address
assigned to the instance.

New file backed storage devices can be added to the guest using the
drive option.  --drive requires at least two parameters.  The first is
the location of the file backed storage, e.g., the location on the
host of a qcow2 file.  The second indicates the format of that storage.
A user can specify additional options which are passed straight
through to the underlying hypervisor.  For example, let's suppose we
have a qcow2 file called $HOME/img.qcow2.  We could make this file
accessible as a block device in our VM as follows.

./ccloudvm create --drive $HOME/img.qcow2,qcow2,aio=threads

The drive will appear as a device, e.g., /dev/vdc in the VM.

Multiple --mount, --drive and --port options can be provided and it's
also possible to override the existing values.  Existing mounts, ones
specified in the instance data document, can be overridden by
specifying an existing tag with new options, existing ports can be
overridden by mapping a new host port to an existing guest port and
existing drives can be overridden by providing a new set of options for
an existing drive path.  For example,

./ccloudvm create --mount hostgo,none,$HOME/go -port 10023-22 xenial

changes the security model of the mount with the hostgo tag and makes the instance
available via ssh on HOSTIP:10023.


### delete \[instance-name\]

ccloudvm delete, shuts down and deletes all the files associated with the VM.

### instances

ccloudvm instances, displays information about the existing instances, e.g.,

```
$ ccloudvm instances
Name			HostIP		Workload	VCPUs	Mem		Disk	
alarmed-agravain	127.3.232.2	xenial		1	1024 MiB	16 Gib
tense-peles		127.3.232.1	xenial		2	2048 MiB	10 Gib
```

### status \[instance-name\]

ccloudvm status provides information about the current ccloudvm VM, e.g., whether
it is running, and how to connect to it.  For example,

```
$ ccloudvm status tense-peles
Name	:	tense-peles
HostIP	:	127.3.232.1
Workload:	xenial
Status	:	VM up
SSH	:	ssh -q -F /dev/null -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -o IdentitiesOnly=yes -i /home/markus/.ccloudvm/id_rsa 127.3.232.1 -p 10022
VCPUs	:	2
Mem	:	2048 MiB
Disk	:	10 GiB
```

### stop \[instance-name\]

ccloudvm stop is used to power down a ccloudvm VM cleanly.

### start \[instance-name\]

ccloudvm start boots a previously created but not running ccloudvm VM.
The start command also supports the --mem and --cpu options.  So it's
possible to change the resources assigned to the guest VM by stopping it
and restarting it, specifying --mem and --cpu.  It's also possible to
specify additional port mappings or mounts via the start command.

Any parameters you pass to the start command override the parameters
you originally passed to create.  These settings are also persisted.
For example, if you were to run

```
./ccloudvm create --mem=2 xenial
./ccloudvm stop
./ccloudvm start --mem=1
./ccloudvm stop
./ccloudvm start
```

The final ccloudvm instance would boot with 1GB of RAM even though no mem
parameter was provided.

The -qemuport option can be used to follow qemu boot logs by connecting to user defined port or an entry in the workload yaml
file.

For example,

ccloudvm start -qemuport 9999

will let you track qemu logs by running the below command

nc localhost 9999

### quit \[instance-name\]

ccloudvm quit terminates the VM immediately.  It does not shut down the OS
running in the VM cleanly.

### setup

The setup command installs any needed dependencies and enables a
systemd user service.  ccloudvm is actually a very simple command line
tool.  It delegates most of the work to a systemd user service.  This
service is launched by socket activation and only runs when needed.
If it has no work to do it quits.

### teardown

The ccloudvm teardown command serves two purposes:

1. it deletes all existing instances
2. it stops the systemd user service, disables it and deletes all the unit files associated with it

An example of its use is as follows:

```
$ ccloudvm teardown
Deleting alarmed-agravain
Deleting tense-peles
Removing ccloudvm service
```

Once you run ccloudvm teardown, ccloudvm will be unusable until you run ccloudvm setup.
