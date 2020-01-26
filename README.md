# mpcoord
Discovery and Coordination of nodes to run MPCs

# Setup

1. Install bazel from https://github.com/bazelbuild/bazel/releases/tag/0.29.1

That's it.
The other steps below will run automatically on-demand:

2. Install the MPC software next to this directory.

`git clone https://github.com/google/private-join-and-compute ../private-join-and-compute`

3. Build it.

`make mpc-build`

# Usage

Compute the intersection-sum of files `local/server_data.csv` and `local/client_data.csv`

1. Run a relay:

`make relay`

The address of the relay will be written in `local/relay.p2p`.

2. Run multiple nodes:

`make node`
`make node`
