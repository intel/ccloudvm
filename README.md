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

- Go 1.9 or greater.

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
$ go get github.com/intel/ccloudvm
$ ccloudvm setup
$ ccloudvm create xenial
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
$ ccloudvm connect
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
$ ccloudvm connect vague-nimue
```

If there is only one ccloudvm instance in existence the name is optional and
need not be specified when issuing most commands.  However, if you have created
two or more instances you will need to specify an instance name when issuing
ccloudvm commands so ccloudvm knows which instance to operate on.

You can delete the VM you've just created by running,

```
$ ccloudvm delete
```

## Workloads

Each VM is created from a workload. A workload is a text file which contains
a set of instructions for initialising the VM.  A workload, among other things,
can specify:

- The resources to be consumed by the VM, e.g., VCPUs, memory, disk space
- The base image from which the VM is to be created
- The folders that should be shared between the host and the VM
- Any file backed storage that should appear as a device in the VM
- An annotated [cloud-init](https://cloudinit.readthedocs.io/en/latest/)
  file that contains the set of instructions
  to run on the first boot of the VM.  This file is used to create
  user accounts, install packages and configure the VM.

ccloudvm ships with a number of workloads for creating VMs based on standard images,
such as Ubuntu 16.04 and Fedora 25.  Users are also free to create their own workloads.
Standard workloads are stored in $GOPATH/src/github.com/intel/ccloudvm/workloads.
User created workloads are stored in ~/.ccloudvm/workloads.  ccloudvm always checks the
~/.ccloudvm/workloads directory first so if a workload exists in both directories
with the same name, ccloudvm will use the workload in ~/.ccloudvm/workloads.

When creating a new instance via the create command the user must specify a workload.
This can be done by providing the name of a workload, present in one of the two directories
mentioned above, a URI pointing to either a local or a remote file, or an absolute path.

When specifying a workload by name the .yaml extension should be omitted. For example,
the command,

```
$ ccloudvm create xenial
```

creates a new instance from the workload
$GOPATH/src/github.com/intel/ccloudvm/workloads/xenial.yaml.

Three schemes are supported when specifying a workload via a URI;
http, https and file.  Note that workloads fetched from URIs are not
stored locally in the two workload directories mentioned above.  Each
time this option is used, ccloudvm will try to retrieve the workload
from the remote location.

An absolute path can also be specified.  This is equivalent to using
the file scheme. For example, to create a workload using the
file /home/x/workload.yaml we have two options.

Using the file scheme:

```
$ ccloudvm create file:///home/ccloudvm/workload.yaml
```

or the absolute path:

```
$ ccloudvm create  /home/ccloudvm/workload.yaml
```

## Creating new Workloads

ccloudvm workloads are multi-doc YAML files containing two documents.
The first document describes how to create and boot instances.  The
second is a cloud-init file containing a list of instructions for
configuring the guest OS running inside the instances.

An example workload is shown below:

```
---
base_image_url: https://cloud-images.ubuntu.com/xenial/current/xenial-server-cloudimg-amd64-disk1.img
base_image_name: Ubuntu 16.04
vm:
  mem_mib: 2048
  disk_gib: 16
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

This workload can be used to create new instances from the Ubuntu 16.04 cloud images.
These instances will be assigned 2 GiB of RAM, 2 VCPUs and 16 GiB for their root file
systems.  A new user will be created inside the guest OS using the user
name of the user on the host computer that invoked ccloudvm.

Note that ccloudvm only works with images that are designed to run
cloud-init on their first boot.  Typically, these are the cloud
images, e.g.,. the [Ubuntu Cloud
Images](https://cloud-images.ubuntu.com/).

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
 - UID            : The UID of the user who invoked ccloudvm
 - GID            : The GID of the user who invoked ccloudvm
 - PublicKey      : The public key that will be used to access the instance over SSH
 - GitUserName    : The git user name of the user who ran ccloudvm
 - GitEmail       : The git email address of the user who ran ccloudvm
 - Mounts         : A slice of mounts, each describing a path to be shared between guest and host
 - Hostname       : The instance hostname
 - UUID           : A UUID for the new instance
 - PackageUpgrade : Indicates whether package upgrade should be performed during the first boot.

As an example consider the second document in the workload definition above.  The User and
PublicKey fields are accessed via the {{.User}} and {{.PublicKey}} Go templates.  Abstracting
all of the user and instance specific information via templates in this way allows us to keep our
workload definitions generic.

### The Instance Specification Document

The first yaml document is called the instance specification document.  It defines the fixed
characteristics of instances created from the workload which cannot be altered.  Three
fields are currently defined:

- base_image_url  : The URL of the qcow2 image upon which instances of the workload should be based.
- base_image_name : Friendly name for the base image.  This is optional.
- vm              : Contains information about creating and booting the instance.

The vm field supports a number of child fields.

- mem_mib    : Number of mebibytes to assign to the VM.  Defaults to 1024 MiBs.
- disk_gib   : Number of gibibytes to assign to the rootfs of the VM.  Defaults to 60 GiB. 
- cpus       : Number of CPUs to assign to the VM.  Defaults to 1 VCPU.
- ports      : Sequence of port objects which map host ports to guest ports
- mounts     : Sequence of mount objects which describe the folders shared between the host and the guest
- drives     : Sequence of drive objects which identify resources accessible on the host to be made available as block devices on the guest.

Each instance is associated with one of the host's IP addresses.  Only one instance can be
associated with one host IP address at any one time.  The user can specify which host IP
address to use for an instance when the instance is created using the --hostip option.
If no host IP address is specified, ccloudvm will select an unused IP address from the
subnet 127.0.0.0/8.  This is usually what you want unless you need to expose a guest service
to devices other than your host.

Each port object has two members, host and guest.  They are both
integers and they specify the mapping of port numbers from host IP
address of the instance to the guest.  A default mapping of 10022 to
22 is always added so that users can connect to their VMs via ccloudvm
connect.  An example port mapping is shown below:

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

The second document contains a cloud-init user data file that can be used
to create users, mount folders, install and update packages and perform
general configuration tasks on the newly created VM.  The user is
expected to be familiar with the
[cloudinit file format](https://cloudinit.readthedocs.io/en/latest/).

The cloudinit document is processed by the Go template engine before
being passed to cloudinit running inside the guest VM.  The template
engine makes a number of functions available to the cloudinit
document.  These functions are used to perform common tasks such as
configuring proxies and communicating with ccloudvm running on the
host.  Each function must be passed the workspace object as the first
parameter.  The following functions are available:

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

As previously mentioned, mounts specified in the instance data document will only
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

### Workload Inheritance

ccloudvm ships with some basic workloads for common Linux distributions such
as Ubuntu 16.04 and Fedora 25.  These workloads contain a number of generic
instructions that create a user, configure proxies and arrange for any
shared host folders to be mounted in the guest.  When creating a new workload
it's convenient to be able to re-use all of this functionality.  This could
be done by simply copying your base workload of choice into a new workload file
and modifying it.  However, there's a much nicer way of doing this.  ccloudvm
allows you to create a new workload by inheriting from an existing workload.
This is done by specifying the inherits field in the first document of the workload.
When workload B inherits from workload A, the contents of workload A
are merged into the contents of workload B.  If there is a conflict, i.e.,
workload A and workload B specify different values for the same field, workload B,
the inheriting workload, takes priority.

```
---
inherits: xenial
vm:
  disk_gib: 20
  ports:
    - host: 8000
      guest: 80
...
---
package_update: true
packages:
 - apache2
...
```

In this example we are creating a new workload that inherits from the
existing xenial workload.  It request a 20 GiB rootfs disk, overriding
the 16 GiB allocation requested by the parent.  It adds a port mapping
from 8000 on the instance's host IP address to 80 on the guest's,
updates the package cache and installs a new package, apache2.  As
the workload inherits from the xenial workload, instances created from
this workload will have a user account created for them, will have
their proxies, if any, set up correctly and any mounts specified at
creation time will be automatically mounted in the guest.

There are a few last things to note when using workload inheritance.
You can only inherit from a workload stored in one of the two standard
workload directories, i.e., it's not possible to specify a URI or an
absolute path when using the inherits field.  Workload hierarchies are
supported, i.e., arbitrary levels of inheritance are supported.  There
is however no multiple inheritance.  A workload can only directly
inherit from one other workload.

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

By default, ccloudvm will assign 1 GiB of memory, 1 VCPU and 60 GiB of disk space
to each new instance it creates.  These resource allocations can be overridden
both in the workload specification and on the command line using the --mem, --cpu,
and --disk options.  For example,

```
$ ccloudvm create --cpus 2 --mem 2048 --disk 10 xenial
```

Creates and boots a VM with 2 VCPUs, 2 GiB of RAM and a rootfs of max 10 GiB.

The --package-upgrade option can be used to provide a hint to workloads
indicating whether packages contained within the base image should be updated or not
during the first boot.  Updating packages can be quite time consuming
and may not be necessary if the user just wants to create a throw away
VM to test something under a particular distro.  This hint only works if
the workloads define the following in their cloudinit documents.

```
package_upgrade: {{with .PackageUpgrade}}{{.}}{{else}}false{{end}}
```
The --qemuport option can be used to follow qemu boot logs by connecting to user defined port or an entry in the workload yaml
file.

For example,

--qemuport=9999

will let you track qemu logs by running the below command

nc localhost 9999

You can also allow a login via this port in case ssh fails to work by modifying the workload file and changing lock_passwd to false and providing a passwd: "....." entry following that.

#### Port mappings, Mounts and Drives

Each new instance created by ccloudvm is assigned a host IP address on
which guest services can be exposed to the host and in some cases the
outside world.  When creating a new instance you can specify the host
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

```
$ ccloudvm create --mount docs,passthrough,$HOME/Documents --port 10000-80 xenial
```

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

```
$ ccloudvm create --drive $HOME/img.qcow2,qcow2,aio=threads
```

The drive will appear as a device, e.g., /dev/vdc in the VM.

Multiple --mount, --drive and --port options can be provided and it's
also possible to override the existing values.  Existing mounts, ones
specified in the instance data document, can be overridden by
specifying an existing tag with new options, existing ports can be
overridden by mapping a new host port to an existing guest port and
existing drives can be overridden by providing a new set of options for
an existing drive path.  For example,

```
$ ccloudvm create --mount hostgo,none,$HOME/go -port 10023-22 xenial
```

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

### run \[instance-name\]

The run command can be used to execute a command on a running guest instance
without having to manually SSH into the guest and type the command.  The
first argument to the run command is the instance name.  This argument is
always required even if there is only one instance.  All subsequent arguments
are passed to the guest to be executed.  For example,

```
$ ccloudvm run gloomy-arthur "lsb_release --release"
Release:	16.04
```

Note that it's best to quote the command that is to be executed on the guest, if
that command contains more than one word.

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
$ ccloudvm create --mem=2 xenial
$ ccloudvm stop
$ ccloudvm start --mem=1
$ ccloudvm stop
$ ccloudvm start
```

The final ccloudvm instance would boot with 1GB of RAM even though no mem
parameter was provided.

The -qemuport option can be used to follow qemu boot logs by connecting to user defined port or an entry in the workload yaml
file.

For example,

```
$ ccloudvm start -qemuport 9999
```

will let you track qemu logs by running the below command

```
$ nc localhost 9999
```

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
