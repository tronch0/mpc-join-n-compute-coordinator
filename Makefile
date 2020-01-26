## Proxy with classic client and server:
server:
	go run mpcoord.go

client:
	go run mpcoord.go -c `cat local/server.p2p`

## Proxy with a relay and nodes, and discovery.
relay:
	go run mpcoord.go -R

node:
	go run mpcoord.go -r `cat local/relay.p2p`

## Events, choose a backend (test or MPC):
outgoing-connection: mpc-client

incoming-connection: mpc-server

## MPC Backend:
mpc-client: mpc-build
	../private-join-and-compute/bazel-bin/client \
		--port=0.0.0.0:$${PORT-10000} \
		--client_data_file=local/client_data.csv

mpc-server: mpc-build
	../private-join-and-compute/bazel-bin/server \
		--port=0.0.0.0:$${PORT-10000} \
		--server_data_file=local/server_data.csv

## Testing Backend:
test-client:
	echo REQUEST | nc localhost $${PORT-10000}

test-server:
	echo RESPONSE | nc -l $${PORT-10000}


## Helper to install and build private-join-and-compute
mpc-install: ../private-join-and-compute/.git

../private-join-and-compute/.git:
	git clone https://github.com/google/private-join-and-compute ../private-join-and-compute

mpc-build: mpc-install ../private-join-and-compute/bazel-bin/server

../private-join-and-compute/bazel-bin/server:
	cd ../private-join-and-compute && \
	bazel build :all --incompatible_disable_deprecated_attr_params=false --incompatible_depset_is_not_iterable=false --incompatible_new_actions_api=false --incompatible_no_support_tools_in_action_inputs=false

mpc-dummy-data: mpc-build
	../private-join-and-compute/bazel-bin/generate_dummy_data \
		--server_data_size=5 --server_data_file=local/server_data.csv \
		--client_data_size=5 --client_data_file=local/client_data.csv \
		--intersection_size=2 --max_associated_value=100
