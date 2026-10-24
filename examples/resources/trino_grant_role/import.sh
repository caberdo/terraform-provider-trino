# Import ID: <role_catalog>.<role>:<grantee_type>:<grantee>
#
# Import writes state only. Trino has no general read path for another
# principal's role grants, so nothing is verified against the server and
# admin_option is imported as false. A "." in the catalog name or a ":" in any
# segment makes the ID ambiguous.
terraform import trino_grant_role.analyst_to_alice 'hive.analyst:USER:alice@example.com'
