#!/bin/bash

set -e

function finish {
    error_code=$?
    set +e

    if [ $created -eq 1 ]
    then
	ccloudvm delete semaphore
    fi

    echo ""
    echo "=============================="
    if [[ $error_code -eq 0 ]]
    then
	echo "=          SUCCESS           ="
    else
	echo "=          FAILURE           ="
    fi
    echo "=============================="
}

trap finish EXIT

created=0

if [[ ! -z "${SEMAPHORE_REPO_SLUG}" ]]
then
    echo ""
    echo "===== Cloning repo ====="
    echo ""

    if [ "$SEMAPHORE_REPO_SLUG" != "intel/ccloudvm" ]
    then
	mkdir -p $GOPATH/src/github.com/intel
	mv $GOPATH/src/github.com/${SEMAPHORE_REPO_SLUG} $GOPATH/src/github.com/intel/ccloudvm
	cd $GOPATH/src/github.com/intel/ccloudvm
	git checkout ${BRANCH_NAME}
    fi

    go get -d -t ./...

    echo ""
    echo "===== Installing packages ====="
    echo ""

    sudo apt-get update
    sudo apt-get install qemu xorriso -y
    sudo chmod ugo+rwx /dev/kvm
fi

echo ""
echo "===== Building ccloudvm ====="
echo ""

go version
go get github.com/intel/ccloudvm
go get github.com/intel/ccloudvm/ccvm

export PATH=$GOPATH/bin:$PATH

if [[ ! -z "${SEMAPHORE_REPO_SLUG}" ]]
then
    ccvm --systemd=false &
else
    ccloudvm setup
fi

# Create and boot a semaphore instance

echo ""
echo "===== Creating instance ====="
echo ""

if [[ ! -z "${SEMAPHORE_REPO_SLUG}" ]]
then
    # There's a race condition here when running on semaphore as we don't
    # when the ccvm server is up and running.  As we can't fork in Go we
    # can't easily create a proper daemon.  It's doable but lots of work
    # and as this is only a test feature we'll just retry a few times.
    retry=0

    set +e
    until [ $retry -ge 10 ]
    do
	ccloudvm create --name semaphore --debug --port "8000-80" --package-upgrade=false semaphore
	if [ $? -eq 0 ]
	then
	    set -e
	    break
	fi

	echo "Retrying create instance"

	let retry=retry+1

	if [ $retry -eq 9 ]
	then
	    set -e
	fi
	sleep 1
    done
else
    ccloudvm create --name semaphore --debug --port "8000-80" --package-upgrade=false semaphore
fi

created=1

echo ""
echo "===== Testing SSH ====="
echo ""

# SSH to the instance and execute a command to determine the remote user

lsb_release_cmd="ccloudvm run semaphore -- lsb_release -c -s"
remote_distro=`$lsb_release_cmd`
echo "Check $remote_distro == xenial"
test $remote_distro = "xenial"

echo ""
echo "===== Testing port mapping ====="
echo ""

# Get port mapping is working by send a http GET to the nginx server running in
# the VM

instance_ip=$(ccloudvm status semaphore | sed '2q;d' | cut -d ":" -f 2 | xargs)

http_proxy= curl http://$instance_ip:8000

# Stop the VM

echo ""
echo "===== Testing ccloudvm stop ====="
echo ""

ccloudvm stop semaphore

# Check there are no qemu processes running.  A bit racy I know.  It would
# be better if stop waited until the qemu process had actually exited.
#
# For the time being we're only going to enable this test inside semaphore
# builds as it could easily fail on a development machine.

if [[ ! -z "${SEMAPHORE_REPO_SLUG}" ]]
then
    retry=0
    until [ $retry -ge 12 ]
    do
	if ! pidof qemu-system-x86_64
	then
	    break
	fi

	let retry=retry+1
	sleep 5
    done

    if [ $retry -eq 12 ]
    then
	echo "qemu process did not exit"
	false
    fi
fi

# Delete the instance

echo ""
echo "===== Testing ccloudvm delete ====="
echo ""

ccloudvm delete semaphore

created=0

# Check it's really gone

set +e
ccloudvm status semaphore 2> /dev/null
error_code=$?
set -e
if [[ $error_code -eq 0 ]]
then
    echo "instance still exists"
    false
fi

# Run the unit tests

if [ "$SEMAPHORE_REPO_SLUG" = "intel/ccloudvm" ]
then
    go get github.com/mattn/goveralls
    $GOPATH/bin/goveralls --race -v -service=semaphore --package github.com/intel/ccloudvm/ccvm
else
    go test --race -v github.com/intel/ccloudvm/ccvm
fi
