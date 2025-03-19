#!/bin/bash
set -xeuo pipefail

# # Find the most recent AL2 tag
# most_recent_al2=$(printf '%s\n' "${all_tags[@]}" | grep '^2\.' | grep -v minimal | grep -v arm | grep -v amd | sort -V | tail -n 1)

# Find the most recent AL2 tag
most_recent_al2=$(./scripts/most_recent_al2.sh)

# Read the JSON file
json_file="linux.version"
json_content=$(cat "$json_file")

# Extract values using jq
version=$(echo "$json_content" | jq -r '.linux.version')
fluent_bit_version=$(echo "$json_content" | jq -r '.linux."fluent-bit"')
cloudwatch_plugin_version=$(echo "$json_content" | jq -r '.linux."cloudwatch-plugin"')
kinesis_plugin_version=$(echo "$json_content" | jq -r '.linux."kinesis-plugin"')
firehose_plugin_version=$(echo "$json_content" | jq -r '.linux."firehose-plugin"')

changelog_commit=$(git log -n1 --pretty=format:%h CHANGELOG.md)
current_commit=$(git log -n1 --pretty=format:%h)

compared_string=""

if [ "$changelog_commit" != "$current_commit" ]; then
    compared_string="
Compared to the previous release, this release adds:"
    commit_names=$(git log --pretty=format:%s "$changelog_commit".."$current_commit")
    if [ -n "${FLUENT_BIT_DIRECTORA+'exists'}"]; then
        latest_release_commit_date=$(git log -n1 --format=%ci)
        # Get commits from the upstream as well
        # Add newline to separate from existing commits
        commit_names+="
"
        # Add FLB commit names to the list considered for changelog updates
        commit_names+="$(cd $FLUENT_BIT_DIRECTORY; git log --pretty=format:%s --after '$latest_release_commit_date')"
    fi
    original_ifs=$IFS
    IFS="
"
    for line in $commit_names
    do
        IFS=" "
        found_label=false
        for word in $line
        do
            # Look for an indication of what this commit is about
            # unless we already are at the stage of collecting
            # the name
            if [ "$found_label" = false ]; then
                # Factor out setting this to true if a
                # label is found, reducing boilerplate
                found_label=true
                if [ "$word" = "feature:" ]; then
                    compared_string+="
* Feature - "
                    continue
                elif [ "$word" = "enhancement:" ]; then
                    compared_string+="
* Enhancement - "
                    continue
                elif [ "$word" = "fix:" ] || [ "$word" = "bugfix:" ]; then
                    compared_string+="
* Fix - "
                    continue
                elif [ "$word" = "Fix" ]; then
                    compared_string+="
* Fix - "
                    # We don't continue for the enclosing elif,
                    # i.e. we add "Fix" as the first word
                elif [ $("$word" | grep -q -x "\(aws:|out_cloudwatch_logs:|out_s3:|out_kinesis:\)") ]; then
                    compared_string+="
* Enhancement - "
                    continue
                else
                    # No label was found
                    found_label=false
                fi
            fi
            if [ "$found_label" = false ]; then
                # If no label, skip this commit
                break 1
            else
                # Add each word aside from the prefix to the commit changelog description
                compared_string+=$word
                compared_string+=" "
            fi
        done
        IFS="
"
    done
    # Append newline to separate from the next changelog entry
    compared_string+="
"
    IFS=$original_ifs
fi

# Generate the changelog entry

new_changelog="
### $version
This release includes:
* Fluent Bit [$fluent_bit_version](https://github.com/fluent/fluent-bit/tree/v$fluent_bit_version)
* Amazon CloudWatch Logs for Fluent Bit ${cloudwatch_plugin_version#v}
* Amazon Kinesis Streams for Fluent Bit ${kinesis_plugin_version#v}
* Amazon Kinesis Firehose for Fluent Bit ${firehose_plugin_version#v}
* Amazon Linux base container image version: $most_recent_al2
$compared_string"

heads=$(head -n 1 CHANGELOG.md)
tails=$(tail -n +3 CHANGELOG.md)

rm -f temp_changelog.txt
echo "$heads" >> temp_changelog.txt
echo "$new_changelog" >> temp_changelog.txt
echo "$tails" >> temp_changelog.txt

mv -f temp_changelog.txt CHANGELOG.md
rm -f temp_changelog.txt


# mv temp_changelog.txt CHANGELOG.md
