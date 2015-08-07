#!/usr/bin/env bash
[ -z "$1" ] && echo "usage: $0 <report_file>" && exit 1
mkdir reports
for email in $(grep -Po "[a-zA-Z0-9_.+-]+@[a-zA-Z0-9-]+\.[a-zA-Z0-9-.]+" $1 | sort | uniq)
do
    grep "$email" "$1" |sort -k2,2 -k1,1 > reports/${email}
done
