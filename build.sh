#!/bin/bash
cd uniqush
gomake clean
gomake
gomake install
cd ..
gomake clean
gomake
