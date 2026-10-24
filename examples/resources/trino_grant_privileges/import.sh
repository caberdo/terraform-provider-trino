# Import a table privilege grant with
# <catalog>.<schema>.<table>:<grantee_type>:<grantee>.
terraform import trino_grant_privileges.analyst_select hive.sales.orders:ROLE:analyst
