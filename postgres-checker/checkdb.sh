#!/usr/bin/env bash

PGUSERNAME=${PGUSERNAME:-tribefire}
PGPASSWORD=${PGPASSWORD:-tribefire}
PGDATABASE=${PGDATABASE:-postgres}
PGHOST=${PGHOST:-localhost}
PGCONNECT_TIMEOUT=${PGCONNECT_TIMEOUT:-1}

MAX_RETRIES=${MAX_RETRIES:-10}

echo "Checking postgres availability via ${PGHOST}..."

wait=0
max_wait=MAX_RETRIES
rc=$(PGCONNECT_TIMEOUT=${PGCONNECT_TIMEOUT} PGPASSWORD=${PGPASSWORD} /usr/bin/psql -d ${PGDATABASE} --host=${PGHOST} --username=${PGUSERNAME} -c "select 'ping'");
until [[ $? == "0" ]] || [[ wait -eq max_wait ]]; do
    delay=$(( wait++ * 2))
    echo "Waiting ${delay} seconds before next retry..."
    echo
    sleep ${delay}
    rc=$(PGCONNECT_TIMEOUT=${PGCONNECT_TIMEOUT} PGPASSWORD=${PGPASSWORD} /usr/bin/psql -d ${PGDATABASE} --host=${PGHOST} --username=${PGUSERNAME} -c "select 'ping'");
done

if [[ wait -eq max_wait ]]; then
    echo "Unable to check postgres availability for ${PGHOST}. Last psql output: ${rc}"
    exit 1
fi

echo "Postgres at ${PGHOST} seems fine"
exit 0